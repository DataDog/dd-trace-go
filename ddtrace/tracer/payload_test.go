// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"bytes"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tinylib/msgp/msgp"
)

var fixedTime = now()

func newSpanList(n int) spanList {
	itoa := map[int]string{0: "0", 1: "1", 2: "2", 3: "3", 4: "4", 5: "5"}
	list := make([]*Span, n)
	for i := 0; i < n; i++ {
		list[i] = newBasicSpan("span.list." + itoa[i%5+1])
		list[i].start = fixedTime
	}
	return list
}

// TestPayloadIntegrity tests that whatever we push into the payload
// allows us to read the same content as would have been encoded by
// the codec.
func TestPayloadIntegrity(t *testing.T) {
	want := new(bytes.Buffer)
	for _, n := range []int{10, 1 << 10, 1 << 17} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			assert := assert.New(t)
			p := newPayload()
			lists := make(spanLists, n)
			for i := 0; i < n; i++ {
				list := newSpanList(i%5 + 1)
				lists[i] = list
				p.push(list)
			}
			want.Reset()
			err := msgp.Encode(want, lists)
			assert.NoError(err)
			assert.Equal(want.Len(), p.size())
			assert.Equal(p.itemCount(), n)

			got, err := io.ReadAll(p)
			assert.NoError(err)
			assert.Equal(want.Bytes(), got)
		})
	}
}

// TestPayloadDecode ensures that whatever we push into the payload can
// be decoded by the codec.
func TestPayloadDecode(t *testing.T) {
	for _, n := range []int{10, 1 << 10} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			assert := assert.New(t)
			p := newPayload()
			for i := 0; i < n; i++ {
				p.push(newSpanList(i%5 + 1))
			}
			var got spanLists
			err := msgp.Decode(p, &got)
			assert.NoError(err)
		})
	}
}

func BenchmarkPayloadThroughput(b *testing.B) {
	b.Run("10K", benchmarkPayloadThroughput(1))
	b.Run("100K", benchmarkPayloadThroughput(10))
	b.Run("1MB", benchmarkPayloadThroughput(100))
}

// benchmarkPayloadThroughput benchmarks the throughput of the payload by subsequently
// pushing a trace containing count spans of approximately 10KB in size each, until the
// payload is filled.
func benchmarkPayloadThroughput(count int) func(*testing.B) {
	return func(b *testing.B) {
		p := newUnsafePayload()
		s := newBasicSpan("X")
		s.meta["key"] = strings.Repeat("X", 10*1024)
		trace := make(spanList, count)
		for i := 0; i < count; i++ {
			trace[i] = s
		}
		b.ReportAllocs()
		b.ResetTimer()
		reset := func() {
			p.header = make([]byte, 8)
			p.off = 8
			atomic.StoreUint32(&p.count, 0)
			p.buf.Reset()
		}
		for i := 0; i < b.N; i++ {
			reset()
			for p.size() < payloadMaxLimit {
				p.push(trace)
			}
		}
	}
}

// TestPayloadConcurrentAccess tests that payload operations are safe for concurrent use
func TestPayloadConcurrentAccess(t *testing.T) {
	p := newPayload()

	// Create some test spans
	spans := make(spanList, 10)
	for i := 0; i < 10; i++ {
		spans[i] = newBasicSpan("test-span")
	}

	var wg sync.WaitGroup

	// Start multiple goroutines that perform concurrent operations
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			// Push some spans
			for j := 0; j < 5; j++ {
				_ = p.push(spans)
			}

			// Read size and item count concurrently
			for j := 0; j < 10; j++ {
				_ = p.size()
				_ = p.itemCount()
			}
		}()
	}

	// Also perform operations from the main goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			_ = p.size()
		}
	}()

	wg.Wait()

	// Verify the payload is in a consistent state
	if p.itemCount() == 0 {
		t.Error("Expected payload to have items after concurrent operations")
	}

	if p.size() <= 0 {
		t.Error("Expected payload size to be positive after concurrent operations")
	}
}

// TestPayloadConcurrentReadWrite tests concurrent read and write operations
func TestPayloadConcurrentReadWrite(t *testing.T) {
	p := newPayload()

	// Add some initial data
	span := newBasicSpan("test")
	spans := spanList{span}
	_ = p.push(spans)

	var wg sync.WaitGroup

	// Concurrent writers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_ = p.push(spans)
			}
		}()
	}

	// Concurrent readers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, 1024)
			for j := 0; j < 10; j++ {
				p.reset()
				_, _ = p.Read(buf)
			}
		}()
	}

	// Concurrent size/count checkers
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = p.size()
				_ = p.itemCount()
			}
		}()
	}

	wg.Wait()

	// Verify final state
	if p.itemCount() == 0 {
		t.Error("Expected payload to have items")
	}
}

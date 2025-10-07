// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/version"
	"github.com/stretchr/testify/assert"
	"github.com/tinylib/msgp/msgp"
)

var fixedTime = now()

// creates a simple span list with n spans
func newSpanList(n int) spanList {
	itoa := map[int]string{0: "0", 1: "1", 2: "2", 3: "3", 4: "4", 5: "5"}
	list := make([]*Span, n)
	for i := 0; i < n; i++ {
		list[i] = newBasicSpan("span.list." + itoa[i%5+1])
		list[i].start = fixedTime
	}
	return list
}

// creates a list of n spans, populated with SpanLinks, SpanEvents, and other fields
func newDetailedSpanList(n int) spanList {
	itoa := map[int]string{0: "0", 1: "1", 2: "2", 3: "3", 4: "4", 5: "5"}
	list := make([]*Span, n)
	for i := 0; i < n; i++ {
		list[i] = newBasicSpan("span.list." + itoa[i%5+1])
		list[i].start = fixedTime
		list[i].service = "service." + itoa[i%5+1]
		list[i].resource = "resource." + itoa[i%5+1]
		list[i].error = int32(i % 2)
		list[i].SetTag("tag."+itoa[i%5+1], "value."+itoa[i%5+1])
		// list[i].spanLinks = []SpanLink{{TraceID: 1, SpanID: 1}, {TraceID: 2, SpanID: 2}}
		// list[i].spanEvents = []spanEvent{{Name: "span.event." + itoa[i%5+1]}}
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
			p := newPayload(traceProtocolV04)
			lists := make(spanLists, n)
			for i := 0; i < n; i++ {
				list := newSpanList(i%5 + 1)
				lists[i] = list
				_, _ = p.push(list)
			}
			want.Reset()
			err := msgp.Encode(want, lists)
			assert.NoError(err)
			stats := p.stats()
			assert.Equal(want.Len(), stats.size)
			assert.Equal(n, stats.itemCount)

			got, err := io.ReadAll(p)
			assert.NoError(err)
			assert.Equal(want.Bytes(), got)
		})
	}
}

// TestPayloadV04Decode ensures that whatever we push into a v0.4 payload can
// be decoded by the codec.
func TestPayloadV04Decode(t *testing.T) {
	for _, n := range []int{10, 1 << 10} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			assert := assert.New(t)
			p := newPayload(traceProtocolV04)
			for i := 0; i < n; i++ {
				_, _ = p.push(newSpanList(i%5 + 1))
			}
			var got spanLists
			err := msgp.Decode(p, &got)
			assert.NoError(err)
		})
	}
}

// TestPayloadV1Decode ensures that whatever we push into a v1 payload can
// be decoded by the codec, and that it matches the original payload.
func TestPayloadV1Decode(t *testing.T) {
	for _, n := range []int{10, 1 << 10} {
		t.Run("simple"+strconv.Itoa(n), func(t *testing.T) {
			var (
				assert = assert.New(t)
				p      = newPayloadV1()
			)
			p.SetContainerID("containerID")
			p.SetLanguageName("go")
			p.SetLanguageVersion("1.25")
			p.SetTracerVersion(version.Tag)
			p.SetRuntimeID(globalconfig.RuntimeID())
			p.SetEnv("test")
			p.SetHostname("hostname")
			p.SetAppVersion("appVersion")

			for i := 0; i < n; i++ {
				_, _ = p.push(newSpanList(i%5 + 1))
			}

			encoded, err := io.ReadAll(p)
			assert.NoError(err)

			got := newPayloadV1()
			buf := bytes.NewBuffer(encoded)
			_, err = buf.WriteTo(got)
			assert.NoError(err)

			o, err := got.decodeBuffer()
			assert.NoError(err)
			assert.Empty(o)
			assert.Equal(p.fields, got.fields)
			assert.Equal(p.containerID, got.containerID)
			assert.Equal(p.languageName, got.languageName)
			assert.Equal(p.languageVersion, got.languageVersion)
			assert.Equal(p.tracerVersion, got.tracerVersion)
			assert.Equal(p.runtimeID, got.runtimeID)
			assert.Equal(p.env, got.env)
			assert.Equal(p.hostname, got.hostname)
			assert.Equal(p.appVersion, got.appVersion)
			assert.Equal(p.fields, got.fields)
		})

		t.Run("detailed"+strconv.Itoa(n), func(t *testing.T) {
			var (
				assert = assert.New(t)
				p      = newPayloadV1()
			)

			for i := 0; i < n; i++ {
				_, _ = p.push(newDetailedSpanList(i%5 + 1))
			}
			encoded, err := io.ReadAll(p)
			assert.NoError(err)

			got := newPayloadV1()
			buf := bytes.NewBuffer(encoded)
			_, err = buf.WriteTo(got)
			assert.NoError(err)

			_, err = got.decodeBuffer()
			assert.NoError(err)
			assert.NotEmpty(got.attributes)
		})
	}
}

func TestPayloadV1EmbeddedStreamingStringTable(t *testing.T) {
	p := newPayloadV1()
	p.SetHostname("production")
	p.SetEnv("production")
	p.SetLanguageName("go")

	assert := assert.New(t)
	encoded, err := io.ReadAll(p)
	assert.NoError(err)

	got := newPayloadV1()
	buf := bytes.NewBuffer(encoded)
	_, err = buf.WriteTo(got)
	assert.NoError(err)

	o, err := got.decodeBuffer()
	assert.NoError(err)
	assert.Empty(o)
	assert.Equal(p.languageName, got.languageName)
	assert.Equal(p.hostname, got.hostname)
	assert.Equal(p.env, got.env)
}

func TestPayloadV1UpdateHeader(t *testing.T) {
	testCases := []uint32{ // Number of items
		15,
		math.MaxUint16,
		math.MaxUint32,
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("n=%d", tc), func(t *testing.T) {
			var (
				p = payloadV1{
					fields: tc,
					header: make([]byte, 8),
				}
				expected []byte
			)
			expected = msgp.AppendMapHeader(expected, tc)
			p.updateHeader()
			if got := p.header[p.readOff:]; !bytes.Equal(expected, got) {
				t.Fatalf("expected %+v, got %+v", expected, got)
			}
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
		p := newPayloadV04()
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
			for p.stats().size < payloadMaxLimit {
				_, _ = p.push(trace)
			}
		}
	}
}

// TestPayloadConcurrentAccess tests that payload operations are safe for concurrent use
func TestPayloadConcurrentAccess(t *testing.T) {
	p := newPayload(traceProtocolV04)

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
				_, _ = p.push(spans)
			}

			// Read size and item count concurrently
			for j := 0; j < 10; j++ {
				stats := p.stats()
				_ = stats.size
				_ = stats.itemCount
			}
		}()
	}

	// Also perform operations from the main goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			_ = p.stats().size
		}
	}()

	wg.Wait()

	// Verify the payload is in a consistent state
	if p.stats().itemCount == 0 {
		t.Error("Expected payload to have items after concurrent operations")
	}

	if p.stats().size <= 0 {
		t.Error("Expected payload size to be positive after concurrent operations")
	}
}

// TestPayloadConcurrentReadWrite tests concurrent read and write operations
func TestPayloadConcurrentReadWrite(t *testing.T) {
	p := newPayload(traceProtocolV04)

	// Add some initial data
	span := newBasicSpan("test")
	spans := spanList{span}
	_, _ = p.push(spans)

	var wg sync.WaitGroup

	// Concurrent writers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_, _ = p.push(spans)
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
				stats := p.stats()
				_ = stats.size
				_ = stats.itemCount
			}
		}()
	}

	wg.Wait()

	// Verify final state
	if p.stats().itemCount == 0 {
		t.Error("Expected payload to have items")
	}
}

func BenchmarkPayloadPush(b *testing.B) {
	sizes := []struct {
		name     string
		numSpans int
		spanSize int
	}{
		{"1span_1KB", 1, 1},
		{"5span_1KB", 5, 1},
		{"10span_1KB", 10, 1},
		{"1span_10KB", 1, 10},
		{"5span_10KB", 5, 10},
		{"10span_50KB", 10, 50},
	}

	for _, size := range sizes {
		b.Run(size.name, func(b *testing.B) {
			spans := make(spanList, size.numSpans)
			for i := 0; i < size.numSpans; i++ {
				span := newBasicSpan("benchmark-span")
				span.meta["data"] = strings.Repeat("x", size.spanSize*1024)
				spans[i] = span
			}

			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				p := newPayloadV04()
				_, _ = p.push(spans)
			}
		})
	}
}

func BenchmarkPayloadStats(b *testing.B) {
	tests := []struct {
		name      string
		numTraces int
		spansPer  int
	}{
		{"empty", 0, 0},
		{"small_1trace_1span", 1, 1},
		{"medium_10trace_5span", 10, 5},
		{"large_100trace_10span", 100, 10},
	}

	for _, test := range tests {
		b.Run(test.name, func(b *testing.B) {
			p := newPayload(traceProtocolV04)

			for i := 0; i < test.numTraces; i++ {
				spans := make(spanList, test.spansPer)
				for j := 0; j < test.spansPer; j++ {
					spans[j] = newBasicSpan("test-span")
				}
				_, _ = p.push(spans)
			}

			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				stats := p.stats()
				_ = stats.size
				_ = stats.itemCount
			}
		})
	}
}

func BenchmarkPayloadConcurrentAccess(b *testing.B) {
	concurrencyLevels := []int{1, 2, 4, 8}

	for _, concurrency := range concurrencyLevels {
		b.Run(fmt.Sprintf("concurrency_%d", concurrency), func(b *testing.B) {
			p := newPayload(traceProtocolV04)
			span := newBasicSpan("concurrent-test")
			spans := spanList{span}

			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				var wg sync.WaitGroup

				for j := 0; j < concurrency; j++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						_, _ = p.push(spans)
					}()
				}

				for j := 0; j < concurrency; j++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						_ = p.stats()
					}()
				}

				wg.Wait()
				p.clear()
			}
		})
	}
}

func TestMsgsizeAnalysis(t *testing.T) {
	sizes := []int{1, 5, 10}
	for _, numSpans := range sizes {
		spans := make(spanList, numSpans)
		for i := 0; i < numSpans; i++ {
			span := newBasicSpan("test")
			span.meta["data"] = strings.Repeat("x", 1024)
			spans[i] = span
		}

		msgsize := spans.Msgsize()
		t.Logf("%d spans with 1KB each: msgsize=%d bytes", numSpans, msgsize)
	}
}

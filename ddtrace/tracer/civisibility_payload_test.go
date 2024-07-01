// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package tracer

import (
	"bytes"
	"io"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tinylib/msgp/msgp"
)

func newCiVisibilityEventsList(n int) []*ciVisibilityEvent {
	list := make([]*ciVisibilityEvent, n)
	for i := 0; i < n; i++ {
		s := newBasicSpan("span.list." + strconv.Itoa(i%5+1))
		s.Start = fixedTime
		list[i] = getCiVisibilityEvent(s)
	}

	return list
}

// TestCiVisibilityPayloadIntegrity tests that whatever we push into the payload
// allows us to read the same content as would have been encoded by
// the codec.
func TestCiVisibilityPayloadIntegrity(t *testing.T) {
	want := new(bytes.Buffer)
	for _, n := range []int{10, 1 << 10, 1 << 17} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			assert := assert.New(t)
			p := newCiVisibilityPayload()
			var allEvents ciVisibilityEvents

			for i := 0; i < n; i++ {
				list := newCiVisibilityEventsList(i%5 + 1)
				allEvents = append(allEvents, list...)
				for _, event := range list {
					p.push(event)
				}
			}

			want.Reset()
			err := msgp.Encode(want, allEvents)
			assert.NoError(err)
			assert.Equal(want.Len(), p.size())
			assert.Equal(p.itemCount(), len(allEvents))

			got, err := io.ReadAll(p)
			assert.NoError(err)
			assert.Equal(want.Bytes(), got)
		})
	}
}

// TestCiVisibilityPayloadDecode ensures that whatever we push into the payload can
// be decoded by the codec.
func TestCiVisibilityPayloadDecode(t *testing.T) {
	assert := assert.New(t)
	for _, n := range []int{10, 1 << 10} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			p := newCiVisibilityPayload()
			for i := 0; i < n; i++ {
				list := newCiVisibilityEventsList(i%5 + 1)
				for _, event := range list {
					p.push(event)
				}
			}
			var got ciVisibilityEvents
			err := msgp.Decode(p, &got)
			assert.NoError(err)
		})
	}
}

func BenchmarkCiVisibilityPayloadThroughput(b *testing.B) {
	b.Run("10K", benchmarkCiVisibilityPayloadThroughput(1))
	b.Run("100K", benchmarkCiVisibilityPayloadThroughput(10))
	b.Run("1MB", benchmarkCiVisibilityPayloadThroughput(100))
}

// benchmarkCiVisibilityPayloadThroughput benchmarks the throughput of the payload by subsequently
// pushing a list of civisibility events containing count spans of approximately 10KB in size each, until the
// payload is filled.
func benchmarkCiVisibilityPayloadThroughput(count int) func(*testing.B) {
	return func(b *testing.B) {
		p := newCiVisibilityPayload()
		s := newBasicSpan("X")
		s.Meta["key"] = strings.Repeat("X", 10*1024)
		e := getCiVisibilityEvent(s)
		events := make(ciVisibilityEvents, count)
		for i := 0; i < count; i++ {
			events[i] = e
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
			for _, event := range events {
				for p.size() < payloadMaxLimit {
					p.push(event)
				}
			}
		}
	}
}

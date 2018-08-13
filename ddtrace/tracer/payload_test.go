// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadog.com/).
// Copyright 2018 Datadog, Inc.

package tracer

import (
	"bytes"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/tinylib/msgp/msgp"
)

func TestPackedSpans(t *testing.T) {
	t.Run("integrity", func(t *testing.T) {
		// whatever we push into the packedSpans should allow us to read the same content
		// as would have been encoded by the encoder.
		ss := new(packedSpans)
		buf := new(bytes.Buffer)
		for _, n := range []int{10, 1 << 10, 1 << 17} {
			t.Run(strconv.Itoa(n), func(t *testing.T) {
				ss.reset()
				spanList := makeTrace(n)
				for _, span := range spanList {
					if err := ss.add(span); err != nil {
						t.Fatal(err)
					}
				}
				buf.Reset()
				err := msgp.Encode(buf, spanList)
				if err != nil {
					t.Fatal(err)
				}
				if ss.count != uint64(n) {
					t.Fatalf("count mismatch: expected %d, got %d", ss.count, n)
				}
				got := ss.buffer().Bytes()
				if len(got) == 0 {
					t.Fatal("0 bytes")
				}
				if !bytes.Equal(buf.Bytes(), got) {
					t.Fatalf("content mismatch")
				}
			})
		}
	})

	t.Run("size", func(t *testing.T) {
		ss := new(packedSpans)
		if ss.size() != 0 {
			t.Fatalf("expected 0, got %d", ss.size())
		}
		if err := ss.add(&span{SpanID: 1}); err != nil {
			t.Fatal(err)
		}
		if ss.size() <= 0 {
			t.Fatal("got 0")
		}
	})

	t.Run("decode", func(t *testing.T) {
		// ensure that whatever we push into the span slice can be decoded by the decoder.
		ss := new(packedSpans)
		for _, n := range []int{10, 1 << 10} {
			t.Run(strconv.Itoa(n), func(t *testing.T) {
				ss.reset()
				for i := 0; i < n; i++ {
					if err := ss.add(&span{SpanID: uint64(i)}); err != nil {
						t.Fatal(err)
					}
				}
				var got spanList
				err := msgp.Decode(ss.buffer(), &got)
				if err != nil {
					t.Fatal(err)
				}
			})
		}
	})
}

// makeTrace returns a spanList of size n.
func makeTrace(n int) spanList {
	ddt := make(spanList, n)
	for i := 0; i < n; i++ {
		span := &span{SpanID: uint64(i)}
		ddt[i] = span
	}
	return ddt
}

// idSeed is the starting number from which the generated span IDs are incremented.
var idSeed uint64 = 123

// makeSpan returns a new span having id as the trace ID.
func makeSpan(id uint64) *span {
	atomic.AddUint64(&idSeed, 1)
	return &span{TraceID: id, SpanID: idSeed}
}

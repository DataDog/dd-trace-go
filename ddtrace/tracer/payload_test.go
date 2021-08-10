// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"bytes"
	"io/ioutil"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tinylib/msgp/msgp"
)

var fixedTime = now()

func newSpanList(n int) spanList {
	itoa := map[int]string{0: "0", 1: "1", 2: "2", 3: "3", 4: "4", 5: "5"}
	list := make([]*span, n)
	for i := 0; i < n; i++ {
		list[i] = newBasicSpan("span.list." + itoa[i%5+1])
		list[i].Start = fixedTime
	}
	return list
}

// TestPayloadIntegrity tests that whatever we push into the payload
// allows us to read the same content as would have been encoded by
// the codec.
func TestPayloadIntegrity(t *testing.T) {
	assert := assert.New(t)
	want := new(bytes.Buffer)
	for _, n := range []int{10, 1 << 10, 1 << 17} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
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

			got, err := ioutil.ReadAll(p)
			assert.NoError(err)
			assert.Equal(want.Bytes(), got)
		})
	}
}

// TestPayloadDecode ensures that whatever we push into the payload can
// be decoded by the codec.
func TestPayloadDecode(t *testing.T) {
	assert := assert.New(t)
	for _, n := range []int{10, 1 << 10} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
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
// pushing a trace containing count spans of approximately 10KB in size each.
func benchmarkPayloadThroughput(count int) func(*testing.B) {
	return func(b *testing.B) {
		p := newPayload()
		s := newBasicSpan("X")
		s.Meta["key"] = strings.Repeat("X", 10*1024)
		trace := make(spanList, count)
		for i := 0; i < count; i++ {
			trace[i] = s
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			for p.size() < payloadMaxLimit {
				p.push(trace)
			}
		}
	}
}

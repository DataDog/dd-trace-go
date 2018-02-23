package tracer

import (
	"encoding/json"
	"io/ioutil"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/ugorji/go/codec"
)

func TestJSONEncoding(t *testing.T) {
	assert := assert.New(t)

	testCases := []struct {
		traces int
		size   int
	}{
		{1, 1},
		{3, 1},
		{1, 3},
		{3, 3},
	}

	for _, tc := range testCases {
		payload := getTestTrace(tc.traces, tc.size)
		r, err := encode(encodingJSON, payload)
		if err != nil {
			t.Fatal(err)
		}
		// decode to check the right encoding
		var traces [][]*span
		err = json.NewDecoder(r).Decode(&traces)
		assert.Nil(err)
		assert.Len(traces, tc.traces)

		for _, trace := range traces {
			assert.Len(trace, tc.size)
			span := trace[0]
			assert.Equal(uint64(42), span.TraceID)
			assert.Equal(uint64(52), span.SpanID)
			assert.Equal(uint64(42), span.ParentID)
			assert.Equal("web", span.Type)
			assert.Equal("high.throughput", span.Service)
			assert.Equal("sending.events", span.Name)
			assert.Equal("SEND /data", span.Resource)
			assert.Equal(int64(1481215590883401105), span.Start)
			assert.Equal(int64(1000000000), span.Duration)
			assert.Equal("192.168.0.1", span.Meta["http.host"])
			assert.Equal(float64(41.99), span.Metrics["http.monitor"])
		}
	}
}

func TestMsgpackEncoding(t *testing.T) {
	assert := assert.New(t)

	testCases := []struct {
		traces int
		size   int
	}{
		{1, 1},
		{3, 1},
		{1, 3},
		{3, 3},
	}

	for _, tc := range testCases {
		payload := getTestTrace(tc.traces, tc.size)
		r, err := encode(encodingMsgpack, payload)
		if err != nil {
			t.Fatal(err)
		}
		// decode to check the right encoding
		var traces [][]*span
		var mh codec.MsgpackHandle
		err = codec.NewDecoder(r, &mh).Decode(&traces)
		assert.Nil(err)
		assert.Len(traces, tc.traces)

		for _, trace := range traces {
			assert.Len(trace, tc.size)
			span := trace[0]
			assert.Equal(uint64(42), span.TraceID)
			assert.Equal(uint64(52), span.SpanID)
			assert.Equal(uint64(42), span.ParentID)
			assert.Equal("web", span.Type)
			assert.Equal("high.throughput", span.Service)
			assert.Equal("sending.events", span.Name)
			assert.Equal("SEND /data", span.Resource)
			assert.Equal(int64(1481215590883401105), span.Start)
			assert.Equal(int64(1000000000), span.Duration)
			assert.Equal("192.168.0.1", span.Meta["http.host"])
			assert.Equal(float64(41.99), span.Metrics["http.monitor"])
		}
	}
}

func BenchmarkMsgpackEncoder(b *testing.B) {
	b.Run("small", benchMsgpack(20, 5))
	b.Run("medium", benchMsgpack(50, 50))
	b.Run("large", benchMsgpack(1000, 100))
}

func benchMsgpack(traceCount, spansPerTrace int) func(b *testing.B) {
	v := getTestTrace(traceCount, spansPerTrace)
	return func(b *testing.B) {
		for n := 0; n < b.N; n++ {
			r, err := encode(encodingMsgpack, v)
			if err != nil {
				b.Fatal(err)
			}
			all, err := ioutil.ReadAll(r)
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(all)))
		}
	}
}

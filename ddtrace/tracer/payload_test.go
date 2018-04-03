package tracer

import (
	"bytes"
	"io/ioutil"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/ugorji/go/codec"
)

// TestPayloadIntegrity tests that whatever we push into the payload
// allows us to read the same content as would have been encoded by
// the codec.
func TestPayloadIntegrity(t *testing.T) {
	assert := assert.New(t)
	p := newPayload()
	want := new(bytes.Buffer)
	for _, items := range [][]interface{}{
		{1, 2, 3},
		{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
		{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17},
	} {
		p.reset()
		for _, v := range items {
			p.push(v)
		}
		assert.Equal(p.itemCount(), len(items))
		got, err := ioutil.ReadAll(p)
		assert.NoError(err)
		want.Reset()
		err = codec.NewEncoder(want, &codec.MsgpackHandle{}).Encode(items)
		assert.NoError(err)
		assert.Equal(want.Bytes(), got)
	}
}

// TestPayloadDecodeInts tests that whatever we push into the payload can
// be decoded by the codec.
func TestPayloadDecodeInts(t *testing.T) {
	assert := assert.New(t)
	p := newPayload()
	for _, items := range [][]int64{
		{1, 2, 3},
		{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
		{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17},
	} {
		p.reset()
		for _, v := range items {
			p.push(v)
		}
		var got []int64
		err := codec.NewDecoder(p, &codec.MsgpackHandle{}).Decode(&got)
		assert.NoError(err)
		assert.Equal(items, got)
	}
}

// TestPayloadDecodetests that whatever we push into the payload can
// be decoded by the codec.
func TestPayloadDecode(t *testing.T) {
	assert := assert.New(t)
	p := newPayload()
	type AB struct{ A, B int }
	x := AB{1, 2}
	for _, items := range [][]AB{
		{x, x, x},
		{x, x, x, x, x, x, x, x, x, x, x, x, x, x},
		{x, x, x, x, x, x, x, x, x, x, x, x, x, x, x, x, x, x, x, x, x, x, x, x, x, x, x, x, x, x, x, x, x, x, x, x, x, x, x, x, x, x},
	} {
		p.reset()
		for _, v := range items {
			p.push(v)
		}
		var got []AB
		err := codec.NewDecoder(p, &codec.MsgpackHandle{}).Decode(&got)
		assert.NoError(err)
		assert.Equal(items, got)
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
		trace := make([]*span, count)
		for i := 0; i < count; i++ {
			trace[i] = s
		}
		// get the size of the trace in bytes
		pkg := new(bytes.Buffer)
		if err := codec.NewEncoder(pkg, &codec.MsgpackHandle{}).Encode(trace); err != nil {
			b.Fatal(err)
		}
		b.SetBytes(int64(pkg.Len()))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			p.push(trace)
		}
	}
}

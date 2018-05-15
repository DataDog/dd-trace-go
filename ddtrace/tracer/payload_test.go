package tracer

import (
	"bytes"
	"io/ioutil"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmihailenco/msgpack"
)

// TestPayloadIntegrity tests that whatever we push into the payload
// allows us to read the same content as would have been encoded by
// the codec.
func TestPayloadIntegrity(t *testing.T) {
	assert := assert.New(t)
	p := newPayload()
	want := new(bytes.Buffer)
	for _, n := range []int{10, 1 << 10, 1 << 17} {
		p.reset()
		items := make([]int, n)
		for i := 0; i < n; i++ {
			items[i] = i
			p.push(i)
		}
		assert.Equal(p.itemCount(), n)
		got, err := ioutil.ReadAll(p)
		assert.NoError(err)
		want.Reset()
		err = msgpack.NewEncoder(want).Encode(items)
		assert.NoError(err)
		assert.Equal(want.Bytes(), got)
	}
}

// TestPayloadDecodeInts tests that whatever we push into the payload can
// be decoded by the codec.
func TestPayloadDecodeInts(t *testing.T) {
	assert := assert.New(t)
	p := newPayload()
	for _, n := range []int{10, 1 << 10, 1 << 17} {
		p.reset()
		want := make([]int, n)
		for i := 0; i < n; i++ {
			want[i] = i
			p.push(i)
		}
		var got []int
		err := msgpack.NewDecoder(p).Decode(&got)
		assert.NoError(err)
		assert.Equal(want, got)
	}
}

// TestPayloadDecode ensures that whatever we push into the payload can
// be decoded by the codec.
func TestPayloadDecode(t *testing.T) {
	assert := assert.New(t)
	p := newPayload()
	type AB struct{ A, B int }
	x := AB{1, 2}
	for _, n := range []int{10, 1 << 10, 1 << 17} {
		p.reset()
		want := make([]AB, n)
		for i := 0; i < n; i++ {
			want[i] = x
			p.push(x)
		}
		var got []AB
		err := msgpack.NewDecoder(p).Decode(&got)
		assert.NoError(err)
		assert.Equal(want, got)
	}
}

// TestPayloadSize ensures that payload reports the same size as the
// regular msgpack encoder.
func TestPayloadSize(t *testing.T) {
	p := newPayload()
	for _, n := range []int{10, 1 << 10, 1 << 17} {
		p.reset()
		nums := make([]int, n)
		for i := 0; i < n; i++ {
			nums[i] = i
			p.push(i)
		}
		var buf bytes.Buffer
		err := msgpack.NewEncoder(&buf).Encode(nums)
		assert.Nil(t, err)
		assert.NotZero(t, p.size())
		assert.Equal(t, buf.Len(), p.size())
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
		if err := msgpack.NewEncoder(pkg).Encode(trace); err != nil {
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

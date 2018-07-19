package tracer

import (
	"bytes"
	"io"
	"io/ioutil"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tinylib/msgp/msgp"
)

var fixedTime = now()

func newSpanList(count int) spanList {
	n := count%5 + 1 // max trace size 5
	itoa := map[int]string{0: "0", 1: "1", 2: "2", 3: "3", 4: "4", 5: "5"}
	list := make([]*span, n)
	for i := 0; i < n; i++ {
		list[i] = newBasicSpan("span.list." + itoa[i])
		list[i].Start = fixedTime
	}
	return list
}

// TestPayloadIntegrity tests that whatever we push into the payload
// allows us to read the same content as would have been encoded by
// the codec.
func TestPayloadIntegrity(t *testing.T) {
	assert := assert.New(t)
	p := newPayload()
	want := new(bytes.Buffer)
	for _, n := range []int{10, 1 << 10, 1 << 17} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			p.reset()
			lists := make(spanLists, n)
			for i := 0; i < n; i++ {
				list := newSpanList(i)
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
	p := newPayload()
	for _, n := range []int{10, 1 << 10} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			p.reset()
			for i := 0; i < n; i++ {
				p.push(newSpanList(i))
			}
			var got spanLists
			err := msgp.Decode(p, &got)
			assert.NoError(err)
		})
	}
}

func BenchmarkPayloadThroughput(b *testing.B) {
	b.Run("100KB", benchmarkPayloadPushTrace(1))
	b.Run("1MB", benchmarkPayloadPushTrace(10))
	b.Run("10MB", benchmarkPayloadPushTrace(100))
}

// benchmarkPayloadPushTrace benchmarks adding a trace to the payload by pushing a trace 10 times
// containing count spans of approximately 10KB in size each, then reading the payload.
func benchmarkPayloadPushTrace(count int) func(*testing.B) {
	return func(b *testing.B) {
		p := newPayload()
		s := newBasicSpan("X")
		s.Meta["key"] = strings.Repeat("X", 10*1024)
		trace := make(spanList, count)
		for i := 0; i < count; i++ {
			trace[i] = s
		}
		rs := make([]byte, 2048) // preallocate slice to read into
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			p.reset()
			for i := 0; i < 10; i++ {
				p.push(trace)
			}
			var err error
			for err != io.EOF {
				_, err = p.Read(rs)
			}
		}
	}
}

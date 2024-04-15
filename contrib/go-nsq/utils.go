package nsq

import (
	"bytes"
	"encoding/gob"
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

var bfp = sync.Pool{
	New: func() interface{} { return bytes.NewBuffer(nil) },
}

func getBuf() *bytes.Buffer {
	buf := bfp.Get().(*bytes.Buffer)
	buf.Reset()

	return buf
}

func putBuf(buf *bytes.Buffer) {
	bfp.Put(buf)
}

type __sep__ struct{}

var sep []byte

// after injection data pattern
// sep|origin body|sep|tracing carrier
func inject(span tracer.Span, body []byte) ([]byte, error) {
	if hasSpanContext(body) || span == nil || span.Context() == nil || span.Context().TraceID() <= 0 || span.Context().SpanID() <= 0 {
		return body, nil
	}

	carri := make(tracer.TextMapCarrier)
	err := tracer.Inject(span.Context(), carri)
	if err != nil {
		return nil, err
	}

	buf := getBuf()
	defer putBuf(buf)

	if err = gob.NewEncoder(buf).Encode(carri); err != nil {
		return nil, err
	}

	bts := make([]byte, len(sep)+len(body)+len(sep)+buf.Len())
	i := copy(bts, sep)
	i += copy(bts[i:], body)
	i += copy(bts[i:], sep)
	copy(bts[i:], buf.Bytes())

	return bts, nil
}

func extract(body []byte) (ddtrace.SpanContext, []byte, error) {
	if !hasSpanContext(body) {
		return nil, body, nil
	}

	comb := bytes.Split(body[len(sep):], sep)
	carri := make(tracer.TextMapCarrier)
	if err := gob.NewDecoder(bytes.NewBuffer(comb[1])).Decode(&carri); err != nil {
		return nil, nil, err
	}

	spnctx, err := tracer.Extract(carri)

	return spnctx, comb[0], err
}

func hasSpanContext(body []byte) bool {
	for i, b := range sep {
		if body[i] != b {
			return false
		}
	}

	return bytes.Count(body[len(sep):], sep) == 1
}

func bodySize(body [][]byte) int {
	var size int
	for i := range body {
		size += len(body[i])
	}

	return size
}

func init() {
	buf := bytes.NewBuffer(nil)
	gob.NewEncoder(buf).Encode(__sep__{})
	sep = buf.Bytes()
}

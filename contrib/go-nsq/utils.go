package nsq

import (
	"bytes"
	"encoding/gob"
	"sync"

	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace"
	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace/tracer"
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

var sep = [4]byte{'~', '6', '@', 'ß'}

func inject(span tracer.Span, body []byte) ([]byte, error) {
	if span == nil {
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

	bts := make([]byte, 8+len(body)+buf.Len())
	i := copy(bts, sep[:])
	i += copy(bts[i:], body)
	i += copy(bts[i:], sep[:])
	copy(bts[i:], buf.Bytes())

	return bts, nil
}

func extract(body []byte) (ddtrace.SpanContext, []byte, error) {
	if !hasSpanContext(body) {
		return nil, body, nil
	}

	comb := bytes.Split(body, sep[:])
	if len(comb[1]) == 0 {
		return nil, comb[0], nil
	}

	carri := make(tracer.TextMapCarrier)
	err := gob.NewDecoder(bytes.NewBuffer(comb[1])).Decode(&carri)
	if err != nil {
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

	return bytes.Count(body[4:], sep[:]) == 1
}

func bodySize(body [][]byte) int {
	var size int
	for i := range body {
		size += len(body[i])
	}

	return size
}

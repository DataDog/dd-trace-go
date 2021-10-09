package nsq

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"errors"
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

func inject(span ddtrace.Span, body []byte) ([]byte, error) {
	carri := make(tracer.TextMapCarrier)
	err := tracer.Inject(span.Context(), carri)
	if err != nil {
		return nil, err
	}

	buf := getBuf()
	defer putBuf(buf)

	enc := gob.NewEncoder(buf)
	if err = enc.Encode(carri); err != nil {
		return nil, err
	}

	bfl := make([]byte, 4)
	binary.BigEndian.PutUint32(bfl, uint32(buf.Len()))

	bts := make([]byte, 4+buf.Len()+len(body))
	i := copy(bts, bfl)
	i += copy(bts[i:], buf.Bytes())
	copy(bts[i:], body)

	return bts, nil
}

func extract(body []byte) (ddtrace.SpanContext, []byte, error) {
	if len(body) < 4 {
		return nil, body, errors.New("length of message body is too small")
	}

	buf := getBuf()
	defer putBuf(buf)

	l := binary.BigEndian.Uint32(body[:4])
	dec := gob.NewDecoder(bytes.NewBuffer(body[4 : 4+l]))

	carri := make(tracer.TextMapCarrier)
	err := dec.Decode(carri)
	if err != nil {
		return nil, body, err
	}

	spctx, err := tracer.Extract(carri)

	return spctx, body[4+l:], err
}

func bodySize(body [][]byte) int {
	var size int
	for i := range body {
		size += len(body[i])
	}

	return size
}

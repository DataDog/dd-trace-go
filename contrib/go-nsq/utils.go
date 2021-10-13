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

// inject tails the span context binary buffer after original message body.
// spec: length of message|message body|span context
//              4 bits    |            |
func inject(span tracer.Span, body []byte) ([]byte, error) {
	var (
		bs  = len(body)
		bsb = make([]byte, 4)
	)
	binary.BigEndian.PutUint32(bsb, uint32(len(body)))

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

	bts := make([]byte, 4+bs+buf.Len())
	i := copy(bts, bsb)
	i += copy(bts[i:], body)
	copy(bts[i:], buf.Bytes())

	return bts, nil
}

func extract(body []byte) (ddtrace.SpanContext, []byte, error) {
	if len(body) < 4 {
		return nil, nil, errors.New("length of message body is too small")
	}

	bs := int(binary.BigEndian.Uint32(body[:4]))
	msgbody := body[4 : 4+bs]
	if 4+bs == len(body) {
		return nil, msgbody, nil
	}

	carri := make(tracer.TextMapCarrier)
	err := gob.NewDecoder(bytes.NewBuffer(body[4+bs:])).Decode(&carri)
	if err != nil {
		return nil, body, err
	}

	spnctx, err := tracer.Extract(carri)

	return spnctx, msgbody, err
}

func bodySize(body [][]byte) int {
	var size int
	for i := range body {
		size += len(body[i])
	}

	return size
}

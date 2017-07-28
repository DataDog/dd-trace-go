package tracer

import (
	"bytes"
	"encoding/json"

	"github.com/ugorji/go/codec"
)

// Encoder is a generic interface that expects encoding methods for traces and
// services, and a Read() method
type Encoder interface {
	EncodeTraces(traces [][]*Span) error
	EncodeServices(services map[string]Service) error
	Read(p []byte) (int, error)
	ContentType() string
}

var mh codec.MsgpackHandle

// msgpackEncoder encodes a list of traces in Msgpack format
type msgpackEncoder struct {
	buffer      *bytes.Buffer
	encoder     *codec.Encoder
	contentType string
}

func newMsgpackEncoder() *msgpackEncoder {
	buffer := &bytes.Buffer{}
	encoder := codec.NewEncoder(buffer, &mh)

	return &msgpackEncoder{
		buffer:      buffer,
		encoder:     encoder,
		contentType: "application/msgpack",
	}
}

// EncodeTraces serializes the given trace list into the internal buffer,
// returning the error if any.
func (e *msgpackEncoder) EncodeTraces(traces [][]*Span) error {
	e.buffer.Reset()
	return e.encoder.Encode(traces)
}

// EncodeServices serializes a service map into the internal buffer.
func (e *msgpackEncoder) EncodeServices(services map[string]Service) error {
	e.buffer.Reset()
	return e.encoder.Encode(services)
}

// Read values from the internal buffer
func (e *msgpackEncoder) Read(p []byte) (int, error) {
	return e.buffer.Read(p)
}

// ContentType return the msgpackEncoder content-type
func (e *msgpackEncoder) ContentType() string {
	return e.contentType
}

// jsonEncoder encodes a list of traces in JSON format
type jsonEncoder struct {
	buffer      *bytes.Buffer
	encoder     *json.Encoder
	contentType string
}

// newJSONEncoder returns a new encoder for the JSON format.
func newJSONEncoder() *jsonEncoder {
	buffer := &bytes.Buffer{}
	encoder := json.NewEncoder(buffer)

	return &jsonEncoder{
		buffer:      buffer,
		encoder:     encoder,
		contentType: "application/json",
	}
}

// EncodeTraces serializes the given trace list into the internal buffer,
// returning the error if any.
func (e *jsonEncoder) EncodeTraces(traces [][]*Span) error {
	e.buffer.Reset()
	return e.encoder.Encode(traces)
}

// EncodeServices serializes a service map into the internal buffer.
func (e *jsonEncoder) EncodeServices(services map[string]Service) error {
	e.buffer.Reset()
	return e.encoder.Encode(services)
}

// Read values from the internal buffer
func (e *jsonEncoder) Read(p []byte) (int, error) {
	return e.buffer.Read(p)
}

// ContentType return the jsonEncoder content-type
func (e *jsonEncoder) ContentType() string {
	return e.contentType
}

const (
	JSON_ENCODER = iota
	MSGPACK_ENCODER
)

// EncoderPool is a pool meant to share the buffers required to encode traces.
// It naively tries to cap the number of active encoders, but doesn't enforce
// the limit. To use a pool, you should Borrow() for an encoder and then
// Return() that encoder to the pool. Encoders in that pool should honor
// the Encoder interface.
type encoderPool struct {
	encoderType int
	pool        chan Encoder
}

func newEncoderPool(encoderType, size int) (*encoderPool, string) {
	pool := &encoderPool{
		encoderType: encoderType,
		pool:        make(chan Encoder, size),
	}

	// Borrow an encoder to retrieve the default ContentType
	encoder := pool.Borrow()
	pool.Return(encoder)

	contentType := encoder.ContentType()
	return pool, contentType
}

func (p *encoderPool) Borrow() Encoder {
	var encoder Encoder

	select {
	case encoder = <-p.pool:
	default:
		switch p.encoderType {
		case JSON_ENCODER:
			encoder = newJSONEncoder()
		case MSGPACK_ENCODER:
			encoder = newMsgpackEncoder()
		}
	}
	return encoder
}

func (p *encoderPool) Return(e Encoder) {
	select {
	case p.pool <- e:
	default:
	}
}

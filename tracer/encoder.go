package tracer

import (
	"bytes"
	"encoding/json"
	"log"
)

// jsonEncoder encodes a list of spans in JSON format.
type jsonEncoder struct {
	j *json.Encoder // the JSON encoder
	b *bytes.Buffer // the reusable buffer
}

// newJSONEncoder returns a new encoder for the JSON format.
func newJSONEncoder() *jsonEncoder {
	b := &bytes.Buffer{}
	j := json.NewEncoder(b)

	return &jsonEncoder{
		j: j,
		b: b,
	}
}

// Encode returns a byte array related to the marshalling
// of a list of spans. It resets the JSONEncoder internal buffer
// and proceeds with the encoding.
func (e *jsonEncoder) Encode(spans []*Span) error {
	e.b.Reset()
	return e.j.Encode(spans)
}

// Read values from the internal buffer
func (e *jsonEncoder) Read(p []byte) (int, error) {
	return e.b.Read(p)
}

// encoderPool is a pool meant to share the buffers required to encode traces.
// It naively tries to cap the number of active encoders, but doesn't enforce
// the limit.
type encoderPool struct {
	pool chan *jsonEncoder
}

func newEncoderPool(size int) *encoderPool {
	return &encoderPool{pool: make(chan *jsonEncoder, size)}
}

// Borrow returns an available encoders or creates a new one
func (p *encoderPool) Borrow() *jsonEncoder {
	var encoder *jsonEncoder

	select {
	case encoder = <-p.pool:
	default:
		log.Println("[POOL] Creating a new encoder")
		encoder = newJSONEncoder()
	}
	return encoder
}

// Return is called when from the code an Encoder is released in the pool.
func (p *encoderPool) Return(e *jsonEncoder) {
	select {
	case p.pool <- e:
	default:
		log.Println("[POOL] The encoding pool is full")
	}
}

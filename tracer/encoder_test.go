package tracer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestJSONEncoder(t *testing.T) {
	assert := assert.New(t)

	// create a traces list with a single span
	var traces [][]*Span
	var spans []*Span
	span := NewSpan("pylons.request", "pylons", "/", 0, 0, 0, nil)
	span.Start = 0
	spans = append(spans, span)
	traces = append(traces, spans)

	// the encoder must return a valid JSON byte array that ends with a \n
	want := `[[{"name":"pylons.request","service":"pylons","resource":"/","type":"","start":0,"duration":0,"span_id":0,"trace_id":0,"parent_id":0,"error":0}]]`
	want += "\n"

	encoder := newJSONEncoder()
	err := encoder.Encode(traces)
	assert.Nil(err)
	assert.Equal(want, encoder.b.String())
}

func TestJSONRead(t *testing.T) {
	assert := assert.New(t)

	// create a traces list with a single span
	var traces [][]*Span
	var spans []*Span
	span := NewSpan("pylons.request", "pylons", "/", 0, 0, 0, nil)
	span.Start = 0
	spans = append(spans, span)
	traces = append(traces, spans)

	// fill the encoder internal buffer
	encoder := newJSONEncoder()
	_ = encoder.Encode(traces)
	expectedSize := encoder.b.Len()

	// the Read function must be used to get the value of the internal buffer
	buff := make([]byte, expectedSize)
	_, err := encoder.Read(buff)

	// it should match the encoding payload
	want := `[[{"name":"pylons.request","service":"pylons","resource":"/","type":"","start":0,"duration":0,"span_id":0,"trace_id":0,"parent_id":0,"error":0}]]`
	want += "\n"
	assert.Nil(err)
	assert.Equal(want, string(buff))
}

func TestPoolBorrowCreate(t *testing.T) {
	assert := assert.New(t)

	// borrow an encoder from the pool
	pool := newEncoderPool(1)
	encoder := pool.Borrow()
	assert.NotNil(encoder)
}

func TestPoolReturn(t *testing.T) {
	assert := assert.New(t)

	// an encoder can return in the pool
	pool := newEncoderPool(1)
	encoder := newJSONEncoder()
	pool.pool <- encoder
	pool.Return(encoder)

	// the encoder is the one we get before
	returnedEncoder := <-pool.pool
	assert.Equal(returnedEncoder, encoder)
}

func TestPoolReuseEncoder(t *testing.T) {
	assert := assert.New(t)

	// borrow, return and borrow again an encoder from the pool
	pool := newEncoderPool(1)
	encoder := pool.Borrow()
	pool.Return(encoder)
	anotherEncoder := pool.Borrow()
	assert.Equal(anotherEncoder, encoder)
}

func TestPoolSize(t *testing.T) {
	pool := newEncoderPool(1)
	encoder := newJSONEncoder()
	anotherEncoder := newJSONEncoder()

	// put two encoders in the pool with a maximum size of 1
	// doesn't hang the caller
	pool.Return(encoder)
	pool.Return(anotherEncoder)
}

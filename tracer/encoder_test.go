package tracer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestJSONEncoder(t *testing.T) {
	assert := assert.New(t)

	// create a spans list with a single span
	var spans []*Span
	span := newSpan("pylons.request", "pylons", "/", 0, 0, 0, nil)
	span.Start = 0
	spans = append(spans, span)

	// the encoder must return a valid JSON byte array that ends with a \n
	want := `[{"name":"pylons.request","service":"pylons","resource":"/","type":"","start":0,"duration":0,"error":0,"span_id":0,"trace_id":0,"parent_id":0}]`
	want += "\n"

	encoder := newJSONEncoder()
	err := encoder.Encode(spans)
	assert.Nil(err)
	assert.Equal(encoder.b.String(), want)
}

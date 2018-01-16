package opentracing

import (
	"testing"

	ot "github.com/opentracing/opentracing-go"
	"github.com/stretchr/testify/assert"
)

func TestExtractNoContext(t *testing.T) {
	assert := assert.New(t)

	tmp := textMapPropagator{}
	carrier := ot.TextMapCarrier{}

	span, err := tmp.Extract(carrier)

	assert.Nil(span)
	assert.EqualError(err, ot.ErrSpanContextNotFound.Error())
}

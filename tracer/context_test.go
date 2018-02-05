package tracer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSpanContextBaggage(t *testing.T) {
	assert := assert.New(t)

	ctx := &spanContext{}
	ctx = ctx.WithBaggageItem("key", "value")
	assert.Equal("value", ctx.baggage["key"])
}

func TestSpanContextIterator(t *testing.T) {
	assert := assert.New(t)

	baggageIterator := make(map[string]string)
	ctx := spanContext{baggage: map[string]string{"key": "value"}}
	ctx.ForeachBaggageItem(func(k, v string) bool {
		baggageIterator[k] = v
		return true
	})

	assert.Len(baggageIterator, 1)
	assert.Equal("value", baggageIterator["key"])
}

package tracer

import (
	"testing"

	"golang.org/x/net/context"

	"github.com/stretchr/testify/assert"
)

func TestContextWithSpan(t *testing.T) {
	assert := assert.New(t)

	// create a new context with a span
	ctx := context.Background()
	tracer := NewTracer()
	span := tracer.NewSpan("pylons.request", "pylons", "/")
	ctx = ContextWithSpan(ctx, span)

	assert.Equal(ctx.Value(datadogActiveSpanKey), span)
}

func TestSpanFromContext(t *testing.T) {
	assert := assert.New(t)

	// create a new context with a span
	ctx := context.Background()
	tracer := NewTracer()
	expectedSpan := tracer.NewSpan("pylons.request", "pylons", "/")
	ctx = ContextWithSpan(ctx, expectedSpan)

	span, ok := SpanFromContext(ctx)
	assert.True(ok)
	assert.Equal(span, expectedSpan)
}

func TestSpanFromContextNil(t *testing.T) {
	assert := assert.New(t)

	// create a context without a span
	ctx := context.Background()

	span, ok := SpanFromContext(ctx)
	assert.False(ok)
	assert.Nil(span)
}

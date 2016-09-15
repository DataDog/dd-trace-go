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

func TestSpanMissingParent(t *testing.T) {
	assert := assert.New(t)
	tracer := NewTracer()

	// assuming we're in an inner function and we
	// forget the nil or ok checks
	ctx := context.Background()
	span, _ := SpanFromContext(ctx)

	// span is nil according to the API
	child := tracer.NewChildSpan("redis.command", span)
	child.Finish()

	// the child is finished but it's not recorded in
	// the tracer buffer
	assert.True(child.Duration > 0)
	assert.Equal(len(tracer.finishedSpans), 0)
}

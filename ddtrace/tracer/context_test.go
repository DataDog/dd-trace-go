package tracer

import (
	"context"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"

	"github.com/stretchr/testify/assert"
)

func TestContextWithSpan(t *testing.T) {
	want := &span{SpanID: 123}
	ctx := ContextWithSpan(context.Background(), want)
	got, ok := ctx.Value(activeSpanKey).(*span)
	assert := assert.New(t)
	assert.True(ok)
	assert.Equal(got, want)
}

func TestSpanFromContext(t *testing.T) {
	t.Run("regular", func(t *testing.T) {
		want := &span{SpanID: 123}
		ctx := ContextWithSpan(context.Background(), want)
		assert.New(t).Equal(SpanFromContext(ctx), want)
	})
	t.Run("nil", func(t *testing.T) {
		assert.Nil(t, SpanFromContext(context.Background()))
		assert.Nil(t, SpanFromContext(nil))
	})
}

func TestStartSpanFromContext(t *testing.T) {
	tracer, _ := getTestTracer()
	defer tracer.Stop()
	internal.GlobalTracer = tracer

	parent := &span{context: &spanContext{spanID: 123, traceID: 456}}
	pctx := ContextWithSpan(context.Background(), parent)
	child, ctx := StartSpanFromContext(pctx, "http.request", ServiceName("gin"), ResourceName("/"))
	assert := assert.New(t)

	got, ok := child.(*span)
	assert.True(ok)
	gotctx := SpanFromContext(ctx)
	assert.Equal(gotctx, got)
	assert.NotNil(gotctx)

	assert.Equal(uint64(456), got.TraceID)
	assert.Equal(uint64(123), got.ParentID)
	assert.Equal("http.request", got.Name)
	assert.Equal("gin", got.Service)
	assert.Equal("/", got.Resource)
}

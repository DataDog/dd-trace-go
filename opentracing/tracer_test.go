package opentracing

import (
	"testing"

	opentracing "github.com/opentracing/opentracing-go"
	"github.com/stretchr/testify/assert"
)

func TestTracerStartSpan(t *testing.T) {
	assert := assert.New(t)

	config := NewConfiguration()
	tracer, _, _ := NewTracer(config)

	span, ok := tracer.StartSpan("web.request").(*Span)
	assert.True(ok)

	assert.NotEqual(uint64(0), span.Span.TraceID)
	assert.NotEqual(uint64(0), span.Span.SpanID)
	assert.Equal(uint64(0), span.Span.ParentID)
	assert.Equal("web.request", span.Span.Name)
	assert.Equal("opentracing.test", span.Span.Service)
	assert.NotNil(span.Span.Tracer())
}

func TestTracerStartChildSpan(t *testing.T) {
	assert := assert.New(t)

	config := NewConfiguration()
	tracer, _, _ := NewTracer(config)

	root := tracer.StartSpan("web.request")
	child := tracer.StartSpan("db.query", opentracing.ChildOf(root.Context()))
	tRoot, ok := root.(*Span)
	assert.True(ok)
	tChild, ok := child.(*Span)
	assert.True(ok)

	assert.NotEqual(uint64(0), tChild.Span.TraceID)
	assert.NotEqual(uint64(0), tChild.Span.SpanID)
	assert.Equal(tRoot.Span.SpanID, tChild.Span.ParentID)
	assert.Equal(tRoot.Span.TraceID, tChild.Span.ParentID)
}

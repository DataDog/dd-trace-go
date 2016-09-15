package tracer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultTracer(t *testing.T) {
	assert := assert.New(t)

	// the default client must be available
	assert.NotNil(DefaultTracer)

	// package free functions must proxy the calls to the
	// default client
	root := NewSpan("pylons.request", "pylons", "/")
	NewChildSpan("pylons.request", root)
	Disable()
	Enable()
}

func TestNewSpan(t *testing.T) {
	assert := assert.New(t)

	// the tracer must create root spans
	tracer := NewTracer()
	span := tracer.NewSpan("pylons.request", "pylons", "/")
	assert.Equal(span.ParentID, uint64(0))
	assert.Equal(span.Service, "pylons")
	assert.Equal(span.Name, "pylons.request")
	assert.Equal(span.Resource, "/")
}

func TestNewSpanChild(t *testing.T) {
	assert := assert.New(t)

	// the tracer must create child spans
	tracer := NewTracer()
	parent := tracer.NewSpan("pylons.request", "pylons", "/")
	child := tracer.NewChildSpan("redis.command", parent)
	assert.Equal(child.ParentID, parent.SpanID)
	assert.Equal(child.TraceID, parent.TraceID)
}

func TestSpanShareTracer(t *testing.T) {
	assert := assert.New(t)

	// all spans must share the same tracer
	tracer := NewTracer()
	parent := tracer.NewSpan("pylons.request", "pylons", "/")
	child := tracer.NewChildSpan("redis.command", parent)
	assert.Equal(parent.tracer, tracer)
	assert.Equal(child.tracer, tracer)
}

func TestTracerDisabled(t *testing.T) {
	assert := assert.New(t)

	// disable the tracer and be sure that the span is not added
	tracer := NewTracer()
	tracer.Disable()
	span := tracer.NewSpan("pylons.request", "pylons", "/")
	span.Finish()
	assert.Equal(len(tracer.finishedSpans), 0)
}

func TestTracerEnabledAgain(t *testing.T) {
	assert := assert.New(t)

	// disable the tracer and enable it again
	tracer := NewTracer()
	tracer.Disable()
	preSpan := tracer.NewSpan("pylons.request", "pylons", "/")
	preSpan.Finish()
	tracer.Enable()
	postSpan := tracer.NewSpan("pylons.request", "pylons", "/")
	postSpan.Finish()
	assert.Equal(len(tracer.finishedSpans), 1)
}

// Mock Transport
type DummyTransport struct{}

func (t *DummyTransport) Send(spans []*Span) error { return nil }

func BenchmarkTracerAddSpans(b *testing.B) {
	// create a new tracer with a DummyTransport
	tracer := NewTracer()
	tracer.transport = &DummyTransport{}

	for n := 0; n < b.N; n++ {
		span := tracer.NewSpan("pylons.request", "pylons", "/")
		span.Finish()
	}
}

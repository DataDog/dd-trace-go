package opentracing

import (
	"github.com/DataDog/dd-trace-go/tracer"
	ot "github.com/opentracing/opentracing-go"
)

// Tracer is a simple, thin interface for Span creation and SpanContext
// propagation.
type Tracer struct {
	tracer *tracer.Tracer
}

// StartSpan creates, starts, and returns a new Span with the given `operationName`
// A Span with no SpanReference options (e.g., opentracing.ChildOf() or
// opentracing.FollowsFrom()) becomes the root of its own trace.
func (t *Tracer) StartSpan(operationName string, opts ...ot.StartSpanOption) ot.Span {
	// TODO: implementation missing; returning an empty Span to validate OpenTracing API
	return &Span{}
}

// Inject takes the `sm` SpanContext instance and injects it for
// propagation within `carrier`. The actual type of `carrier` depends on
// the value of `format`.
func (t *Tracer) Inject(sp ot.SpanContext, format interface{}, carrier interface{}) error {
	return nil
}

// Extract returns a SpanContext instance given `format` and `carrier`.
func (t *Tracer) Extract(format interface{}, carrier interface{}) (ot.SpanContext, error) {
	return nil, nil
}

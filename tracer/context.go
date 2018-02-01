package tracer

import (
	"context"
)

// SpanContext represents Span state that must propagate to descendant Spans
// and across process boundaries.
type SpanContext struct {
	traceID  uint64
	spanID   uint64
	parentID uint64
	sampled  bool
	span     *OpenSpan
	baggage  map[string]string
}

// ForeachBaggageItem grants access to all baggage items stored in the
// SpanContext
func (c SpanContext) ForeachBaggageItem(handler func(k, v string) bool) {
	for k, v := range c.baggage {
		if !handler(k, v) {
			break
		}
	}
}

// WithBaggageItem returns an entirely new SpanContext with the
// given key:value baggage pair set.
func (c SpanContext) WithBaggageItem(key, val string) SpanContext {
	var newBaggage map[string]string
	if c.baggage == nil {
		newBaggage = map[string]string{key: val}
	} else {
		newBaggage = make(map[string]string, len(c.baggage)+1)
		for k, v := range c.baggage {
			newBaggage[k] = v
		}
		newBaggage[key] = val
	}
	// Use positional parameters so the compiler will help catch new fields.
	return SpanContext{
		traceID:  c.traceID,
		spanID:   c.spanID,
		parentID: c.parentID,
		sampled:  c.sampled,
		span:     c.span,
		baggage:  newBaggage,
	}
}

// OLD ////////////////////////////////

var spanKey = "datadog_trace_span"

// ContextWithSpan will return a new context that includes the given span.
// DEPRECATED: use span.Context(ctx) instead.
func ContextWithSpan(ctx context.Context, span *Span) context.Context {
	if span == nil {
		return ctx
	}
	return span.Context(ctx)
}

// SpanFromContext returns the stored *Span from the Context if it's available.
// This helper returns also the ok value that is true if the span is present.
func SpanFromContext(ctx context.Context) (*Span, bool) {
	if ctx == nil {
		return nil, false
	}
	span, ok := ctx.Value(spanKey).(*Span)
	return span, ok
}

// SpanFromContextDefault returns the stored *Span from the Context. If not, it
// will return an empty span that will do nothing.
func SpanFromContextDefault(ctx context.Context) *Span {

	// FIXME[matt] is it better to return a singleton empty span?
	if ctx == nil {
		return &Span{}
	}

	span, ok := SpanFromContext(ctx)
	if !ok {
		return &Span{}
	}
	return span
}

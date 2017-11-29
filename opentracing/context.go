package opentracing

// SpanContext represents Span state that must propagate to descendant Spans
// and across process boundaries.
type SpanContext struct {
}

// ForeachBaggageItem grants access to all baggage items stored in the
// SpanContext
func (n SpanContext) ForeachBaggageItem(handler func(k, v string) bool) {
	// TODO: implementation required
}

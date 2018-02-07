package tracer

// spanContext represents Span state that must propagate to descendant Spans
// and across process boundaries.
type spanContext struct {
	traceID  uint64
	spanID   uint64
	parentID uint64
	sampled  bool
	span     *span
	baggage  map[string]string
}

// newSpanContext creates a new spanContext with the properties of the given
// span. If the baggage is not nil, it makes a copy of it within the spanContext.
func newSpanContext(span *span, baggage map[string]string) *spanContext {
	context := &spanContext{
		traceID:  span.TraceID,
		spanID:   span.SpanID,
		parentID: span.ParentID,
		sampled:  span.Sampled,
		span:     span,
		baggage:  nil,
	}
	if baggage != nil {
		context.baggage = make(map[string]string, len(baggage))
		for k, v := range baggage {
			context.baggage[k] = v
		}
	}
	return context
}

// ForeachBaggageItem grants access to all baggage items stored in the
// spanContext
func (c *spanContext) ForeachBaggageItem(handler func(k, v string) bool) {
	for k, v := range c.baggage {
		if !handler(k, v) {
			break
		}
	}
}

func (c *spanContext) setBaggageItem(key, val string) {
	if c.baggage == nil {
		c.baggage = make(map[string]string, 1)
	}
	c.baggage[key] = val
}

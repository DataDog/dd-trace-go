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
		c.baggage = map[string]string{}
	}
	c.baggage[key] = val
}

package tracer

// spanContext represents Span state that must propagate to descendant Spans
// and across process boundaries.
type spanContext struct {
	traceID  uint64
	spanID   uint64
	parentID uint64
	sampled  bool
	span     *Span
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

// WithBaggageItem returns an entirely new spanContext with the
// given key:value baggage pair set.
func (c *spanContext) WithBaggageItem(key, val string) *spanContext {
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
	return &spanContext{
		traceID:  c.traceID,
		spanID:   c.spanID,
		parentID: c.parentID,
		sampled:  c.sampled,
		span:     c.span,
		baggage:  newBaggage,
	}
}

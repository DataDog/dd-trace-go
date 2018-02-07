package tracer

import "sync"

var (
	// traceStartSize is the initial size of our trace buffer,
	// by default we allocate for a handful of spans within the trace,
	// reasonable as span is actually way bigger, and avoids re-allocating
	// over and over. Could be fine-tuned at runtime.
	traceStartSize = 10
	// traceMaxSize is the maximum number of spans we keep in memory.
	// This is to avoid memory leaks, if above that value, spans are randomly
	// dropped and ignore, resulting in corrupted tracing data, but ensuring
	// original program continues to work as expected.
	traceMaxSize = int(1e5)
)

// spanContext represents span state that must propagate to descendant spans
// and across process boundaries.
type spanContext struct {
	// this group of fields propagates only locally
	*span        // reference to the span that hosts this context
	*trace       // reference to the trace that this span belongs too
	sampled bool // whether this span will be sampled or not

	// this group of fields propagates cross-process
	traceID  uint64
	spanID   uint64
	parentID uint64
	baggage  map[string]string
}

// newSpanContext creates a new spanContext to serve as context for the given
// span. If the provided parent is not nil, the baggage, sampled value and the
// reference to the trace will be copied over. This method also pushes the given
// span into the trace. As a result, calling it multiple times on the same span
// might have unexpected results.
func newSpanContext(span *span, parent *spanContext) *spanContext {
	context := &spanContext{
		traceID:  span.TraceID,
		spanID:   span.SpanID,
		parentID: span.ParentID,
		sampled:  true,
		span:     span,
		baggage:  nil,
	}
	if parent == nil {
		context.trace = newTrace(span.tracer.pushTrace)
	} else {
		context.trace = parent.trace
		context.sampled = parent.sampled
		baggage := parent.baggage
		context.baggage = make(map[string]string, len(baggage))
		for k, v := range baggage {
			context.baggage[k] = v
		}
	}
	if err := context.trace.push(span); err != nil {
		span.tracer.pushErr(err)
	}
	return context
}

// ForeachBaggageItem implements opentracing.SpanContext
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

// finish marks this span as finished in the trace.
func (c *spanContext) finish() { c.trace.ackFinish() }

// trace holds information about a specific trace. This structure is shared
// between all spans in a trace.
type trace struct {
	mu       sync.RWMutex // guards below fields
	trace    []*span      // all the spans that are part of this trace
	finished int          // the number of finished spans

	// onFinish is a callback function that will be called with the current
	// trace as an argument at the moment when the number of finished spans
	// equals the number of spans in the trace.
	onFinish func([]*span)
}

func newTrace(onFinish func([]*span)) *trace {
	return &trace{
		onFinish: onFinish,
		trace:    make([]*span, 0, traceStartSize),
	}
}

func (t *trace) push(sp *span) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.trace) >= traceMaxSize {
		return &errBufferFull{name: "span buffer", size: len(t.trace)}
	}
	t.trace = append(t.trace, sp)
	return nil
}

// ackFinish aknowledges that another span in the trace has finished, and checks
// if the trace is complete, in which case it calls the onFinish function.
func (t *trace) ackFinish() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.finished++
	if len(t.trace) != t.finished {
		return
	}
	t.onFinish(t.trace)
	t.trace = nil
	t.finished = 0 // important, because a buffer can be used for several flushes
}

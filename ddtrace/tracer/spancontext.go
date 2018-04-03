package tracer

import (
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
)

var _ ddtrace.SpanContext = (*spanContext)(nil)

// SpanContext represents span state that must propagate to descendant spans
// and across process boundaries.
type spanContext struct {
	// the below group should propagate only locally

	trace   *trace // reference to the trace that this span belongs too
	span    *span  // reference to the span that hosts this context
	sampled bool   // whether this span will be sampled or not

	// the below group should propagate cross-process

	traceID  uint64
	spanID   uint64
	parentID uint64

	mu      sync.RWMutex // guards baggage
	baggage map[string]string
}

// newSpanContext creates a new SpanContext to serve as context for the given
// span. If the provided parent is not nil, the context will inherit the trace,
// baggage and other values from it. This method also pushes the span into the
// new context's trace and as a result, it should not be called multiple times
// for the same span.
func newSpanContext(span *span, parent *spanContext) *spanContext {
	context := &spanContext{
		traceID:  span.TraceID,
		spanID:   span.SpanID,
		parentID: span.ParentID,
		sampled:  true,
		span:     span,
	}
	if parent == nil {
		context.trace = newTrace(span.tracer.pushTrace)
	} else {
		context.trace = parent.trace
		context.sampled = parent.sampled
		for k, v := range parent.baggage {
			context.setBaggageItem(k, v)
		}
	}
	// put span in context's trace
	if err := context.trace.push(span); err != nil {
		span.tracer.pushErr(err)
	}
	return context
}

// ForeachBaggageItem implements SpanContext
func (c *spanContext) ForeachBaggageItem(handler func(k, v string) bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for k, v := range c.baggage {
		if !handler(k, v) {
			break
		}
	}
}

func (c *spanContext) setBaggageItem(key, val string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.baggage == nil {
		c.baggage = make(map[string]string, 1)
	}
	c.baggage[key] = val
}

func (c *spanContext) baggageItem(key string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.baggage[key]
}

// finish marks this span as finished in the trace.
func (c *spanContext) finish() { c.trace.ackFinish() }

// trace holds information about a specific trace. This structure is shared
// between all spans in a trace.
type trace struct {
	mu       sync.RWMutex // guards below fields
	spans    []*span      // all the spans that are part of this trace
	finished int          // the number of finished spans

	// onFinish is a callback function that will be called with the current
	// trace as an argument at the moment when the number of finished spans
	// equals the number of spans in the trace.
	onFinish func([]*span)
}

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

// newTrace creates a new trace using the given callback which will be called
// upon completion of the trace.
func newTrace(onFinish func([]*span)) *trace {
	return &trace{
		onFinish: onFinish,
		spans:    make([]*span, 0, traceStartSize),
	}
}

// push pushes a new span into the trace. If the buffer is full, it returns
// a errBufferFull error.
func (t *trace) push(sp *span) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.spans) >= traceMaxSize {
		return &errBufferFull{name: "span buffer", size: len(t.spans)}
	}
	t.spans = append(t.spans, sp)
	return nil
}

// ackFinish aknowledges that another span in the trace has finished, and checks
// if the trace is complete, in which case it calls the onFinish function.
func (t *trace) ackFinish() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.finished++
	if len(t.spans) != t.finished {
		return
	}
	t.onFinish(t.spans)
	t.spans = nil
	t.finished = 0 // important, because a buffer can be used for several flushes
}

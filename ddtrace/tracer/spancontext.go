package tracer

import (
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
)

var _ ddtrace.SpanContext = (*spanContext)(nil)

// SpanContext represents a span state that can propagate to descendant spans
// and across process boundaries. It contains all the information needed to
// spawn a direct descendant of the span that it belongs to. It can be used
// to create distributed tracing by propagating it using the provided interfaces.
type spanContext struct {
	traceID uint64
	spanID  uint64

	mu          sync.RWMutex // guards below fields
	baggage     map[string]string
	priority    int
	hasPriority bool

	// the below group should propagate only locally
	span    *span // reference to the span that hosts this context
	sampled bool  // whether this span will be sampled or not
}

// newSpanContext creates a new SpanContext to serve as context for the given
// span. If the provided parent is not nil, the context will inherit the trace,
// baggage and other values from it.
func newSpanContext(span *span, parent *spanContext) *spanContext {
	context := &spanContext{
		traceID: span.TraceID,
		spanID:  span.SpanID,
		sampled: true,
		span:    span,
	}
	if v, ok := span.Metrics[samplingPriorityKey]; ok {
		context.hasPriority = true
		context.priority = int(v)
	}
	if parent != nil {
		context.sampled = parent.sampled
		context.hasPriority = parent.hasSamplingPriority()
		context.priority = parent.samplingPriority()
		parent.ForeachBaggageItem(func(k, v string) bool {
			context.setBaggageItem(k, v)
			return true
		})
		if parent.span == nil {
			// we mark this span as root because it has parent ID different
			// from zero (distributed trace) and it would be hard to identify
			// as a root otherwise.
			if span.Metrics == nil {
				span.Metrics = make(map[string]float64, 1)
			}
			span.Metrics["_root_span"] = float64(1)
		}
	}
	return context
}

// SpanID implements ddtrace.SpanContext.
func (c *spanContext) SpanID() uint64 { return c.spanID }

// TraceID implements ddtrace.SpanContext.
func (c *spanContext) TraceID() uint64 { return c.traceID }

// ForeachBaggageItem implements ddtrace.SpanContext.
func (c *spanContext) ForeachBaggageItem(handler func(k, v string) bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for k, v := range c.baggage {
		if !handler(k, v) {
			break
		}
	}
}

func (c *spanContext) setSamplingPriority(p int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.priority = p
	c.hasPriority = true
}

func (c *spanContext) samplingPriority() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.priority
}

func (c *spanContext) hasSamplingPriority() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.hasPriority
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

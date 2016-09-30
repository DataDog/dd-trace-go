package tracer

import (
	"log"
	"time"
)

const (
	defaultDeliveryURL = "http://localhost:7777/spans"
	tracerWaitTimeout  = 5 * time.Second
	flushInterval      = 2 * time.Second
)

// Tracer is the common struct we use to collect, buffer
type Tracer struct {
	enabled   bool      // defines if the Tracer is enabled or not
	transport transport // is the transport mechanism used to delivery spans to the agent
	sampler   sampler   // is the trace sampler to only keep some samples

	buffer *spansBuffer

	DebugLoggingEnabled bool
}

// NewTracer returns a Tracer instance that owns a span delivery system. Each Tracer starts
// a new go routing that handles the delivery. It's safe to create new tracers, but it's
// advisable only if the default client doesn't fit your needs.
func NewTracer() *Tracer {
	// initialize the Tracer
	t := &Tracer{
		enabled:             true,
		transport:           newHTTPTransport(defaultDeliveryURL),
		buffer:              newSpansBuffer(spanBufferDefaultMaxSize),
		sampler:             newAllSampler(),
		DebugLoggingEnabled: false,
	}

	// start a background worker
	go t.worker()

	return t
}

// Enable activates the tracer so that Spans are appended in the tracer buffer.
// By default, a tracer is always enabled after the creation.
func (t *Tracer) Enable() {
	t.enabled = true
}

// Disable deactivates the tracer so that Spans are not appended in the tracer buffer.
// This means that *Span can be used as usual but the span.Finish() call will not
// put the span in a buffer.
func (t *Tracer) Disable() {
	t.enabled = false
}

// SetSampleRate sets a sample rate for all the future traces.
// sampleRate has to be between 0 (sample nothing) and 1 (sample everything).
func (t *Tracer) SetSampleRate(sampleRate float64) {
	if sampleRate == 1 {
		t.sampler = newAllSampler()
	} else if sampleRate >= 0 && sampleRate < 1 {
		t.sampler = newRateSampler(sampleRate)
	} else {
		log.Printf("tracer.SetSampleRate rate must be between 0 and 1, now: %f", sampleRate)
	}
}

// NewSpan creates a new root Span with a random identifier. This high-level API is commonly
// used to start a new tracing session.
func (t *Tracer) NewSpan(name, service, resource string) *Span {
	// create and return the Span
	spanID := nextSpanID()
	span := newSpan(name, service, resource, spanID, spanID, 0, t)
	t.sampler.Sample(span)
	return span
}

// NewChildSpan returns a new span that is child of the Span passed as argument.
// This high-level API is commonly used to create a nested span in the current
// tracing session.
func (t *Tracer) NewChildSpan(name string, parent *Span) *Span {
	spanID := nextSpanID()

	// when we're using parenting in inner functions, it's possible that
	// a nil pointer is sent to this function as argument. To prevent a crash,
	// it's better to be defensive and to produce a wrongly configured span
	// that is not sent to the trace agent.
	if parent == nil {
		span := newSpan(name, "", name, spanID, spanID, spanID, t)
		t.sampler.Sample(span)
		return span
	}

	// child that is correctly configured
	span := newSpan(name, parent.Service, name, spanID, parent.TraceID, parent.SpanID, parent.tracer)
	// child sampling same as the parent
	span.Sampled = parent.Sampled

	return span
}

// record queues the finished span for further processing.
func (t *Tracer) record(span *Span) {
	if t.enabled && span.Sampled {
		t.buffer.Push(span)
	}
}

// worker periodically flushes traces to the transport.
func (t *Tracer) worker() {
	for range time.Tick(flushInterval) {

		spans := t.buffer.Pop()

		if t.DebugLoggingEnabled {
			log.Printf("Sending %d spans", len(spans))
			for _, s := range spans {
				log.Printf("SPAN:\n%s", s.String())
			}
		}

		if t.enabled && t.transport != nil && 0 < len(spans) {
			err := t.transport.Send(spans)
			if err != nil {
				log.Printf("[WORKER] flush failed, lost %s spans", err)
			}
		}
	}
}

// DefaultTracer is a default *Tracer instance
var DefaultTracer = NewTracer()

// NewSpan is an helper function that is used to create a RootSpan, through
// the DefaultTracer client. If the default client doesn't fit your needs,
// you can create a new Tracer through the NewTracer function.
func NewSpan(name, service, resource string) *Span {
	return DefaultTracer.NewSpan(name, service, resource)
}

// NewChildSpan is an helper function that is used to create a child Span, through
// the DefaultTracer client. If the default client doesn't fit your needs,
// you can create a new Tracer through the NewTracer function.
func NewChildSpan(name string, parent *Span) *Span {
	return DefaultTracer.NewChildSpan(name, parent)
}

// Enable is an helper function that is used to proxy the Enable() call to the
// DefaultTracer client.
func Enable() {
	DefaultTracer.Enable()
}

// Disable is an helper function that is used to proxy the Disable() call to the
// DefaultTracer client.
func Disable() {
	DefaultTracer.Disable()
}

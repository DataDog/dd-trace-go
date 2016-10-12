package tracer

import (
	"context"
	"log"
	"time"
)

const (
	defaultDeliveryURL = "http://localhost:7777/spans"
	flushInterval      = 2 * time.Second
)

// Tracer creates, buffers and submits Spans which are used to time blocks of
// compuration.
//
// When a tracer is disabled, it will not submit spans for processing.
type Tracer struct {
	transport Transport // is the transport mechanism used to delivery spans to the agent
	sampler   sampler   // is the trace sampler to only keep some samples

	buffer *spansBuffer

	DebugLoggingEnabled bool
	enabled             bool // defines if the Tracer is enabled or not
}

// NewTracer returns a Tracer instance that owns a span delivery system. Each Tracer starts
// a new go routing that handles the delivery. It's safe to create new tracers, but it's
// advisable only if the default client doesn't fit your needs.
func NewTracer() *Tracer {
	// initialize the Tracer
	return NewTracerTransport(NewHTTPTransport(defaultDeliveryURL))
}

// NewTracerTransport create a new Tracer with the given transport.
func NewTracerTransport(transport Transport) *Tracer {
	t := &Tracer{
		enabled:             true,
		transport:           transport,
		buffer:              newSpansBuffer(spanBufferDefaultMaxSize),
		sampler:             newAllSampler(),
		DebugLoggingEnabled: false,
	}

	// start a background worker
	go t.worker()

	return t
}

// SetEnabled will enable or disable the tracer.
func (t *Tracer) SetEnabled(enabled bool) {
	t.enabled = enabled
}

// Enabled returns whether or not a tracer is enabled.
func (t *Tracer) Enabled() bool {
	return t.enabled
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

// NewChildSpan returns a new span that is child of the Span passed as
// argument.
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

// NewChildSpanFromContext returns a new span that is the child of the current
// span in the given context. The program will not crash if the context is nil
// or doesn't contain a span, but it will not have a service specified.
func (t *Tracer) NewChildSpanFromContext(name string, ctx context.Context) *Span {
	span, _ := SpanFromContext(ctx) // tolerate nil spans
	return NewChildSpan(name, span)
}

// record queues the finished span for further processing.
func (t *Tracer) record(span *Span) {
	if t.enabled && span.Sampled {
		t.buffer.Push(span)
	}
}

// Flush will push any currently buffered traces to the server.
func (t *Tracer) Flush() error {
	spans := t.buffer.Pop()

	if t.DebugLoggingEnabled {
		log.Printf("Sending %d spans", len(spans))
		for _, s := range spans {
			log.Printf("SPAN:\n%s", s.String())
		}
	}

	// bal if there's nothing to do
	if !t.enabled || t.transport == nil || len(spans) == 0 {
		return nil
	}

	return t.transport.Send(spans)

}

// worker periodically flushes traces to the transport.
func (t *Tracer) worker() {
	for range time.Tick(flushInterval) {
		err := t.Flush()
		if err != nil {
			log.Printf("[WORKER] flush failed, lost spans: %s", err)
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
	DefaultTracer.SetEnabled(true)
}

// Disable is an helper function that is used to proxy the Disable() call to the
// DefaultTracer client.
func Disable() {
	DefaultTracer.SetEnabled(false)
}

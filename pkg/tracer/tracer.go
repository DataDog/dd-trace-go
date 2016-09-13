package tracer

import (
	"sync"
	"time"

	log "github.com/cihub/seelog"
)

const (
	defaultDeliveryURL = "http://localhost:7777/spans"
	tracerWaitTimeout  = 5 * time.Second
	flushInterval      = 2 * time.Second
)

// Tracer is the common struct we use to collect, buffer
type Tracer struct {
	transport   Transport    // is the transport mechanism used to delivery spans to the agent
	flushTicker *time.Ticker // ticker used to Tick() the flush interval

	finishedSpans []*Span    // a list of finished spans
	mu            sync.Mutex // used to gain/release the lock for finishedSpans array
}

// NewTracer returns a Tracer instance that owns a span delivery system. Each Tracer starts
// a new go routing that handles the delivery. It's safe to create new tracers, but it's
// advisable only if the default client doesn't fit your needs.
func NewTracer() *Tracer {
	// initialize the Tracer
	t := &Tracer{
		transport:   NewHTTPTransport(defaultDeliveryURL),
		flushTicker: time.NewTicker(flushInterval),
	}

	// start a background worker
	go t.worker()
	return t
}

// NewSpan creates a new root Span with a random identifier. This high-level API is commonly
// used to start a new tracing session.
func (t *Tracer) NewSpan(service, name, resource string) *Span {
	// create and return the Span
	spanID := nextSpanID()
	return newSpan(spanID, spanID, 0, service, name, resource, t)
}

// NewChildSpan returns a new span that is child of the Span passed as argument.
// This high-level API is commonly used to create a nested span in the current
// tracing session.
func (t *Tracer) NewChildSpan(parent *Span, service, name, resource string) *Span {
	spanID := nextSpanID()
	return newSpan(spanID, parent.TraceID, parent.SpanID, service, name, resource, t)
}

// record stores the span in the array of finished spans.
func (t *Tracer) record(span *Span) {
	t.mu.Lock()
	t.finishedSpans = append(t.finishedSpans, span)
	t.mu.Unlock()
}

// Background worker that handles data delivery through the Transport instance.
// It waits for a flush interval and then it tries to find an available dispatcher
// if there is something to send.
func (t *Tracer) worker() {
	for _ = range t.flushTicker.C {
		if len(t.finishedSpans) > 0 {
			t.mu.Lock()
			spans := t.finishedSpans
			t.finishedSpans = nil
			t.mu.Unlock()

			err := t.transport.Send(spans)

			if err == nil {
				log.Debugf("[WORKER] flushed %d spans", len(spans))
			} else {
				log.Errorf("[WORKER] flush failed, lost %s spans", err)
			}
		}
	}
}

// DefaultTracer is a default *Tracer instance
var DefaultTracer = NewTracer()

// NewSpan is an helper function that is used to create a RootSpan, through
// the DefaultTracer client. If the default client doesn't fit your needs,
// you can create a new Tracer through the NewTracer function.
func NewSpan(service, name, resource string) *Span {
	return DefaultTracer.NewSpan(service, name, resource)
}

// NewChildSpan is an helper function that is used to create a child Span, through
// the DefaultTracer client. If the default client doesn't fit your needs,
// you can create a new Tracer through the NewTracer function.
func NewChildSpan(parent *Span, service, name, resource string) *Span {
	return DefaultTracer.NewChildSpan(parent, service, name, resource)
}

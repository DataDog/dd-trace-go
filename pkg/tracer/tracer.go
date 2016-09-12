package tracer

import (
	"sync"
	"time"

	log "github.com/cihub/seelog"
)

const (
	defaultDeliveryURL  = "http://localhost:7777/spans"
	numberOfDispatchers = 1
	tracerWaitTimeout   = 5 * time.Second
	flushInterval       = 2 * time.Second
)

// Tracer is the common struct we use to collect, buffer
type Tracer struct {
	Transport Transport    // is the transport mechanism used to delivery spans to the agent
	ticker    *time.Ticker // ticker used to Tick() the flush interval
	dispatch  chan []*Span // the channel that sends a list of spans to the agent

	finishedSpans []*Span    // a list of finished spans
	mu            sync.Mutex // used to gain/release the lock for finishedSpans array

	// A WaitGroup tracks the current status of the message
	// pipeline so that at any time the Tracer and the internal
	// Worker may know if there are messages that are not flushed.
	// The intent is to use it with the tracer.Wait() API to assure that
	// all messages have been transported before exiting the process.
	wg sync.WaitGroup
}

// NewTracer returns a Tracer instance that owns a span delivery system. Each Tracer starts
// a new go routing that handles the delivery. It's safe to create new tracers, but it's
// advisable only if the default client doesn't fit your needs.
// TODO: make possible to create a Tracer with a different Transport system
func NewTracer() *Tracer {
	return &Tracer{
		Transport: NewHTTPTransport(defaultDeliveryURL),
		ticker:    time.NewTicker(flushInterval),
	}
}

// Trace creates a new Span with a random identifier. This high-level API allows to
// create a new root span if the parent attribute is set to nil; otherwise, a child
// of that span is created.
func (t *Tracer) Trace(service, name, resource string, parent *Span) *Span {
	// this check detects if this is the first time we are using this tracer;
	// in that case, initialize the dispatch channel and start a background
	// worker and a pool of dispatchers that manages spans delivery
	if t.dispatch == nil {
		t.dispatch = make(chan []*Span)
		go t.worker()
		for i := 0; i < numberOfDispatchers; i++ {
			go t.dispatcher()
		}
	}

	spanID := nextSpanID()

	if parent == nil {
		// we create a root span
		return newSpan(spanID, spanID, 0, service, name, resource, t)
	}

	// we create a child span
	return newSpan(spanID, parent.TraceID, parent.SpanID, service, name, resource, t)
}

// Wait for the messages delivery. This method assures that all messages have been
// delivered before exiting the process. If for any reasons Wait() hangs for more
// than tracerWaitTimeout, the process exits anyway.
func (t *Tracer) Wait() {
	// the channel will be closed after the Wait() returns
	c := make(chan struct{})
	go func() {
		defer close(c)
		t.wg.Wait()
	}()

	// wait until a timeout elapses
	select {
	case <-c:
	case <-time.After(tracerWaitTimeout):
		log.Warn("Giving up on submitting remaining traces!")
	}
}

// Background worker that handles data delivery through the Transport instance.
// It waits for a flush interval and then it tries to find an available dispatcher
// if there is something to send.
// TODO[manu]: the worker must shutdown if an exit channel is closed
func (t *Tracer) worker() {
	for _ = range t.ticker.C {
		t.mu.Lock()
		if len(t.finishedSpans) > 0 {
			select {
			case t.dispatch <- t.finishedSpans:
				t.wg.Add(1)
				t.finishedSpans = nil
			default:
				// the pool doesn't have an available dispatcher
				// so we try to send the list of spans later
				log.Warn("[WORKER] No available dispatchers. Retrying later.")
			}
		}
		t.mu.Unlock()
	}
}

// Background worker that sends data to the local/remote agent. It listens
// forever the dispatch channel until an exit command is received.
// TODO[manu]: the dispatcher must shutdown if an exit channel is closed
func (t *Tracer) dispatcher() {
	for finishedSpans := range t.dispatch {
		err := t.Transport.Send(finishedSpans)

		if err != nil {
			// TODO[manu]: we have an error during the send and we must
			// decide how to handle such kind of errors
		}

		// notify that this dispatcher has done the job
		log.Infof("[DISPATCHER] flushed %d spans", len(finishedSpans))
		t.wg.Done()
	}
}

// DefaultTracer is a default *Tracer instance
var DefaultTracer = NewTracer()

// Trace is an helper function that is used to create a root span or a child
// span, through the DefaultTracer client. If the default client doesn't fit your needs,
// you can create a new Tracer through the NewTracer function.
func Trace(service, name, resource string, parent *Span) *Span {
	return DefaultTracer.Trace(service, name, resource, parent)
}

// Wait helper function that waits for the message delivery of the
// DefaultClient.
func Wait() {
	DefaultTracer.Wait()
}

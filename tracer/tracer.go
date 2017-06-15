package tracer

import (
	"context"
	"log"
	"math/rand"
	"sync"
	"time"
)

const (
	tickInterval   = 100 * time.Millisecond
	flushInterval  = 2 * time.Second
	traceChanLen   = 1000
	serviceChanLen = 100
	errChanLen     = 100
)

func init() {
	randGen = rand.New(newRandSource())
}

type Service struct {
	Name    string `json:"-"`        // the internal of the service (e.g. acme_search, datadog_web)
	App     string `json:"app"`      // the name of the application (e.g. rails, postgres, custom-app)
	AppType string `json:"app_type"` // the type of the application (e.g. db, web)
}

func (s Service) Equal(s2 Service) bool {
	return s.Name == s2.Name && s.App == s2.App && s.AppType == s2.AppType
}

// Tracer creates, buffers and submits Spans which are used to time blocks of
// compuration.
//
// When a tracer is disabled, it will not submit spans for processing.
type Tracer struct {
	transport Transport // is the transport mechanism used to delivery spans to the agent
	sampler   sampler   // is the trace sampler to only keep some samples

	DebugLoggingEnabled bool
	enabled             bool // defines if the Tracer is enabled or not
	enableMu            sync.RWMutex

	meta   map[string]string
	metaMu sync.RWMutex

	traceChan   chan []*Span
	serviceChan chan Service
	errChan     chan error

	services map[string]Service // name -> service

	exit   chan struct{}
	exitWG *sync.WaitGroup

	forceFlushIn  chan struct{}
	forceFlushOut chan error
}

// NewTracer creates a new Tracer. Most users should use the package's
// DefaultTracer instance.
func NewTracer() *Tracer {
	return NewTracerTransport(newDefaultTransport())
}

// NewTracerTransport create a new Tracer with the given transport.
func NewTracerTransport(transport Transport) *Tracer {
	t := &Tracer{
		enabled:             true,
		transport:           transport,
		sampler:             newAllSampler(),
		DebugLoggingEnabled: false,

		traceChan:   make(chan []*Span, traceChanLen),
		serviceChan: make(chan Service, serviceChanLen),
		errChan:     make(chan error, errChanLen),

		services: make(map[string]Service),

		exit:   make(chan struct{}),
		exitWG: &sync.WaitGroup{},

		forceFlushIn:  make(chan struct{}),
		forceFlushOut: make(chan error),
	}

	// start a background worker
	t.exitWG.Add(1)
	go t.worker()

	return t
}

// Stop stops the tracer.
func (t *Tracer) Stop() {
	close(t.exit)
	t.exitWG.Wait()
}

// SetEnabled will enable or disable the tracer.
func (t *Tracer) SetEnabled(enabled bool) {
	t.enableMu.Lock()
	defer t.enableMu.Unlock()
	t.enabled = enabled
}

// Enabled returns whether or not a tracer is enabled.
func (t *Tracer) Enabled() bool {
	t.enableMu.RLock()
	defer t.enableMu.RUnlock()
	return t.enabled
}

// SetSampleRate sets a sample rate for all the future traces.
// sampleRate has to be between 0.0 and 1.0 and represents the ratio of traces
// that will be sampled. 0.0 means that the tracer won't send any trace. 1.0
// means that the tracer will send all traces.
func (t *Tracer) SetSampleRate(sampleRate float64) {
	if sampleRate == 1 {
		t.sampler = newAllSampler()
	} else if sampleRate >= 0 && sampleRate < 1 {
		t.sampler = newRateSampler(sampleRate)
	} else {
		log.Printf("tracer.SetSampleRate rate must be between 0 and 1, now: %f", sampleRate)
	}
}

// SetServiceInfo update the application and application type for the given
// service.
func (t *Tracer) SetServiceInfo(name, app, appType string) {
	select {
	case t.serviceChan <- Service{
		Name:    name,
		App:     app,
		AppType: appType,
	}:
	default: // non blocking
		select {
		case t.errChan <- &errorServiceChanFull{Len: len(t.serviceChan)}:
		default: // if channel is full, drop & ignore error, better do this than stall program
		}
	}
}

// SetMeta adds an arbitrary meta field at the tracer level.
// This will append those tags to each span created by the tracer.
func (t *Tracer) SetMeta(key, value string) {
	if t == nil { // Defensive, span could be initialized with nil tracer
		return
	}

	t.metaMu.Lock()
	if t.meta == nil {
		t.meta = make(map[string]string)
	}
	t.meta[key] = value
	t.metaMu.Unlock()
}

// getAllMeta returns all the meta set by this tracer.
// In most cases, it is nil.
func (t *Tracer) getAllMeta() map[string]string {
	if t == nil { // Defensive, span could be initialized with nil tracer
		return nil
	}

	var meta map[string]string

	t.metaMu.RLock()
	if t.meta != nil {
		meta = make(map[string]string, len(t.meta))
		for key, value := range t.meta {
			meta[key] = value
		}
	}
	t.metaMu.RUnlock()

	return meta
}

// NewRootSpan creates a span with no parent. Its ids will be randomly
// assigned.
func (t *Tracer) NewRootSpan(name, service, resource string) *Span {
	spanID := NextSpanID()
	span := NewSpan(name, service, resource, spanID, spanID, 0, t)

	span.buffer = newSpanBuffer(t.traceChan, t.errChan, 0, 0)
	t.sampler.Sample(span)
	span.buffer.Push(span)

	return span
}

// NewChildSpan returns a new span that is child of the Span passed as
// argument.
func (t *Tracer) NewChildSpan(name string, parent *Span) *Span {
	spanID := NextSpanID()

	// when we're using parenting in inner functions, it's possible that
	// a nil pointer is sent to this function as argument. To prevent a crash,
	// it's better to be defensive and to produce a wrongly configured span
	// that is not sent to the trace agent.
	if parent == nil {
		span := NewSpan(name, "", name, spanID, spanID, spanID, t)

		// [TODO:christian] write a test to check this code path, ie
		// "NewChilSpan with nil parent"
		span.buffer = newSpanBuffer(t.traceChan, t.errChan, 0, 0)
		t.sampler.Sample(span)
		span.buffer.Push(span)

		return span
	}

	parent.RLock()
	// child that is correctly configured
	span := NewSpan(name, parent.Service, name, spanID, parent.TraceID, parent.SpanID, parent.tracer)
	// child sampling same as the parent
	span.Sampled = parent.Sampled
	span.parent = parent
	span.buffer = parent.buffer
	parent.RUnlock()

	span.buffer.Push(span)

	return span
}

// NewChildSpanFromContext will create a child span of the span contained in
// the given context. If the context contains no span, an empty span will be
// returned.
func (t *Tracer) NewChildSpanFromContext(name string, ctx context.Context) *Span {
	span, _ := SpanFromContext(ctx) // tolerate nil spans
	return t.NewChildSpan(name, span)
}

// NewChildSpanWithContext will create and return a child span of the span contained in the given
// context, as well as a copy of the parent context containing the created
// child span. If the context contains no span, an empty root span will be returned.
// If nil is passed in for the context, a context will be created.
func (t *Tracer) NewChildSpanWithContext(name string, ctx context.Context) (*Span, context.Context) {
	span := NewChildSpanFromContext(name, ctx)
	return span, span.Context(ctx)
}

func (t *Tracer) getTraces() [][]*Span {
	traces := make([][]*Span, 0, len(t.traceChan))

	for {
		select {
		case trace := <-t.traceChan:
			traces = append(traces, trace)
		default: // return when there's no more data
			return traces
		}
	}
}

// flushTraces will push any currently buffered traces to the server.
func (t *Tracer) flushTraces() error {
	traces := t.getTraces()

	if t.DebugLoggingEnabled {
		log.Printf("Sending %d traces", len(traces))
		for _, trace := range traces {
			if len(trace) > 0 {
				log.Printf("TRACE: %d\n", trace[0].TraceID)
				for _, span := range trace {
					log.Printf("SPAN:\n%s", span.String())
				}
			}
		}
	}

	// bal if there's nothing to do
	if !t.Enabled() || t.transport == nil || len(traces) == 0 {
		return nil
	}

	_, err := t.transport.SendTraces(traces)

	return err
}

func (t *Tracer) updateServices() bool {
	servicesModified := false
	for {
		select {
		case service := <-t.serviceChan:
			if s, found := t.services[service.Name]; !found || !s.Equal(service) {
				t.services[service.Name] = service
				servicesModified = true
			}
		default: // return when there's no more data
			return servicesModified
		}
	}
}

// flushTraces will push any currently buffered services to the server.
func (t *Tracer) flushServices() error {
	servicesModified := t.updateServices()

	if !t.Enabled() || !servicesModified {
		return nil
	}

	_, err := t.transport.SendServices(t.services)

	return err
}

// flushErrors will process log messages that were queued
func (t *Tracer) flushErrors() {
	logErrors(t.errChan)
}

func (t *Tracer) flush() error {
	var retErr error

	nbTraces := len(t.traceChan)
	if err := t.flushTraces(); err != nil {
		log.Printf("cannot flush traces: %v", err)
		log.Printf("lost %d traces", nbTraces)
		retErr = err
	}

	if err := t.flushServices(); err != nil {
		log.Printf("cannot flush services: %v", err)
		// give priority to flushTraces error, more important if we have both
		if retErr == nil {
			retErr = err
		}
	}

	t.flushErrors()

	return retErr
}

// ForceFlush forces a flush of data (traces and services) to the agent.
// Flushes are done by a background task on a regular basis, so you never
// need to call this manually, mostly useful for testing and debugging.
func (t *Tracer) ForceFlush() error {
	t.forceFlushIn <- struct{}{}
	return <-t.forceFlushOut
}

// worker periodically flushes traces and services to the transport.
func (t *Tracer) worker() {
	defer t.exitWG.Done()

	flushTicker := time.NewTicker(tickInterval)
	defer flushTicker.Stop()

	lastFlush := time.Now()
	for {
		select {
		case now := <-flushTicker.C:
			// We flush either if:
			// - flushInterval is elapsed since last flush
			// - one of the buffers is at least 50% full
			// One of the reason of doing this is that under heavy load,
			// payloads might get *really* big if we do only time-based flushes.
			// It's not perfect as a trace can have many spans so estimating the
			// number of traces can be misleading.
			if lastFlush.Add(flushInterval).Before(now) ||
				len(t.traceChan) > cap(t.traceChan)/5 || // 200 traces
				len(t.serviceChan) > cap(t.serviceChan)/2 || // 50 services
				len(t.errChan) > cap(t.errChan)/2 { // 50 errors
				t.flush()
				lastFlush = now
			}

		case <-t.forceFlushIn:
			t.forceFlushOut <- t.flush()

		case <-t.exit:
			t.flush()
			return
		}
	}
}

// DefaultTracer is a global tracer that is enabled by default. All of the
// packages top level NewSpan functions use the default tracer.
//
//	span := tracer.NewRootSpan("sql.query", "user-db", "select * from foo where id = ?")
//	defer span.Finish()
//
var DefaultTracer = NewTracer()

// NewRootSpan creates a span with no parent. It's ids will be randomly
// assigned.
func NewRootSpan(name, service, resource string) *Span {
	return DefaultTracer.NewRootSpan(name, service, resource)
}

// NewChildSpan creates a span that is a child of parent. It will inherit the
// parent's service and resource.
func NewChildSpan(name string, parent *Span) *Span {
	return DefaultTracer.NewChildSpan(name, parent)
}

// NewChildSpanFromContext will create a child span of the span contained in
// the given context. If the context contains no span, a span with
// no service or resource will be returned.
func NewChildSpanFromContext(name string, ctx context.Context) *Span {
	return DefaultTracer.NewChildSpanFromContext(name, ctx)
}

// NewChildSpanWithContext will create and return a child span of the span contained in the given
// context, as well as a copy of the parent context containing the created
// child span. If the context contains no span, an empty root span will be returned.
// If nil is passed in for the context, a context will be created.
func NewChildSpanWithContext(name string, ctx context.Context) (*Span, context.Context) {
	return DefaultTracer.NewChildSpanWithContext(name, ctx)
}

// Enable will enable the default tracer.
func Enable() {
	DefaultTracer.SetEnabled(true)
}

// Disable will disable the default tracer.
func Disable() {
	DefaultTracer.SetEnabled(false)
}

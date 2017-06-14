package tracer

import (
	"context"
	"log"
	"math/rand"
	"sync"
	"time"
)

const (
	flushInterval = 2 * time.Second
	traceChanLen  = 10
	errChanLen    = 100
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

	services         map[string]Service // name -> service
	servicesModified bool
	serviceChan      chan Service

	traceChan chan []*Span
	errChan   chan error

	exit   chan struct{}
	exitWG *sync.WaitGroup

	forceFlushIn  chan struct{}
	forceFlushOut chan struct{}
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

		services:    make(map[string]Service),
		serviceChan: make(chan Service, 10), // we don't want to block when a flush is in progress

		exit:   make(chan struct{}),
		exitWG: &sync.WaitGroup{},

		traceChan: make(chan []*Span, traceChanLen),
		errChan:   make(chan error, errChanLen),

		forceFlushIn:  make(chan struct{}),
		forceFlushOut: make(chan struct{}),
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
	t.serviceChan <- Service{
		Name:    name,
		App:     app,
		AppType: appType,
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
	span.buffer = newTraceBuffer(t.traceChan, t.errChan, 0, 0)
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
		t.sampler.Sample(span)
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

func (t *Tracer) traces() [][]*Span {
	traces := make([][]*Span, 0, len(t.traceChan))

	for {
		select {
		case trace := <-t.traceChan:
			traces = append(traces, trace)
		default:
			return traces
		}
	}

	return traces
}

// flushTraces will push any currently buffered traces to the server.
func (t *Tracer) flushTraces() error {
	traces := t.traces()

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

// flushTraces will push any currently buffered services to the server.
func (t *Tracer) flushServices() error {
	if !t.Enabled() || !t.servicesModified {
		return nil
	}

	if _, err := t.transport.SendServices(t.services); err != nil {
		return err
	}

	t.servicesModified = false
	return nil
}

func (t *Tracer) flush() {
	nbTraces := len(t.traceChan)
	if err := t.flushTraces(); err != nil {
		log.Printf("cannot flush traces: %v", err)
		log.Printf("lost %d traces", nbTraces)
	}

	if err := t.flushServices(); err != nil {
		log.Printf("cannot flush services: %v", err)
	}
}

func (t *Tracer) appendService(service Service) {
	if s, found := t.services[service.Name]; !found || !s.Equal(service) {
		t.services[service.Name] = service
		t.servicesModified = true
	}
}

func (t *Tracer) drainServices() {
	for {
		select {
		case service := <-t.serviceChan:
			t.appendService(service)
		default:
			return
		}
	}
}

func (t *Tracer) ForceFlush() {
	t.forceFlushIn <- struct{}{}
	<-t.forceFlushOut
}

// worker periodically flushes traces and services to the transport.
func (t *Tracer) worker() {
	defer t.exitWG.Done()

	flushTicker := time.NewTicker(flushInterval)
	defer flushTicker.Stop()

	for {
		select {
		case <-flushTicker.C:
			t.flush()

		case <-t.forceFlushIn:
			t.flush()
			t.forceFlushOut <- struct{}{}

		case service := <-t.serviceChan:
			t.appendService(service)

		case <-t.exit:
			// serviceChan being buffered, we drain it before the
			// last flush to make sure we have all information. It
			// is an edge case, but it is important for tests.
			t.drainServices()

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

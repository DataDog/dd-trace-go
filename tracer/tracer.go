package tracer

import (
	"log"
	"os"
	"strconv"
	"time"

	"github.com/DataDog/dd-trace-go/tracer/ext"
	"github.com/opentracing/opentracing-go"
)

const flushInterval = 2 * time.Second

type service struct {
	Name    string `json:"-"`        // the internal of the service (e.g. acme_search, datadog_web)
	App     string `json:"app"`      // the name of the application (e.g. rails, postgres, custom-app)
	AppType string `json:"app_type"` // the type of the application (e.g. db, web)
}

func (s service) equals(s2 service) bool {
	return s.Name == s2.Name && s.App == s2.App && s.AppType == s2.AppType
}

const (
	// traceBufferSize is the capacity of the trace channel. This channels is emptied
	// on a regular basis (worker thread) or when it reaches 50% of its capacity.
	// If it's full, then data is simply dropped and ignored, with a log message.
	// This only happens under heavy load,
	traceBufferSize = 1000
	// serviceBufferSize is the length of the service channel. As for the trace channel,
	// it's emptied by worker thread or when it reaches 50%. Note that there should
	// be much less data here, as service data does not be to be updated that often.
	serviceBufferSize = 50
	// errorBufferSize is the number of errors we keep in the error channel. When this
	// one is full, errors are just ignored, dropped, nothing left. At some point,
	// there's already a whole lot of errors in the backlog, there's no real point
	// in keeping millions of errors, a representative sample is enough. And we
	// don't want to block user code and/or bloat memory or log files with redundant data.
	errorBufferSize = 200
)

var _ opentracing.Tracer = (*tracer)(nil)

// Tracer creates, buffers and submits Spans which are used to time blocks of
// compuration.
type tracer struct {
	*config

	// services maps service names to services.
	services map[string]service

	// this group of channels provides a thread-safe way to buffer traces,
	// services and errors before flushing them to the transport.
	traceBuffer   chan []*span
	serviceBuffer chan service
	errorBuffer   chan error

	// these channels represent various requests that the tracer worker can pick up.
	flushAllReq      chan chan<- struct{}
	flushTracesReq   chan struct{}
	flushServicesReq chan struct{}
	flushErrorsReq   chan struct{}

	exitReq chan chan<- struct{}
}

// Start creates a new tracer with the given set of options and registers it as
// the global tracer. The returned stopFunc should be used to cleanly exit the
// program, flushing any leftover traces to the transport. To use the tracer,
// simply use the opentracing API as normal.
func Start(opts ...Option) (stopFunc func()) {
	t := newTracer(opts...)
	opentracing.SetGlobalTracer(t)
	return t.stop
}

func newTracer(opts ...Option) *tracer {
	c := new(config)
	defaults(c)
	for _, fn := range opts {
		fn(c)
	}
	if c.transport == nil {
		c.transport = newTransport(c.agentAddr)
	}
	t := &tracer{
		config:           c,
		services:         make(map[string]service),
		traceBuffer:      make(chan []*span, traceBufferSize),
		serviceBuffer:    make(chan service, serviceBufferSize),
		errorBuffer:      make(chan error, errorBufferSize),
		exitReq:          make(chan chan<- struct{}),
		flushAllReq:      make(chan chan<- struct{}),
		flushTracesReq:   make(chan struct{}, 1),
		flushServicesReq: make(chan struct{}, 1),
		flushErrorsReq:   make(chan struct{}, 1),
	}

	go t.worker()

	return t
}

func (t *tracer) pushTrace(trace []*span) {
	if len(t.traceBuffer) >= cap(t.traceBuffer)/2 { // starts being full, anticipate, try and flush soon
		select {
		case t.flushTracesReq <- struct{}{}:
		default: // a flush was already requested, skip
		}
	}
	select {
	case t.traceBuffer <- trace:
	default: // never block user code
		t.pushErr(&errorTraceChanFull{Len: len(t.traceBuffer)})
	}
}

func (t *tracer) pushService(service service) {
	if len(t.serviceBuffer) >= cap(t.serviceBuffer)/2 { // starts being full, anticipate, try and flush soon
		select {
		case t.flushServicesReq <- struct{}{}:
		default: // a flush was already requested, skip
		}
	}
	select {
	case t.serviceBuffer <- service:
	default: // never block user code
		t.pushErr(&errorServiceChanFull{Len: len(t.serviceBuffer)})
	}
}

func (t *tracer) pushErr(err error) {
	if len(t.errorBuffer) >= cap(t.errorBuffer)/2 { // starts being full, anticipate, try and flush soon
		select {
		case t.flushErrorsReq <- struct{}{}:
		default: // a flush was already requested, skip
		}
	}
	select {
	case t.errorBuffer <- err:
	default:
		// OK, if we get this, our error error buffer is full,
		// we can assume it is filled with meaningful messages which
		// are going to be logged and hopefully read, nothing better
		// we can do, blocking would make things worse.
	}
}

// StartSpan creates, starts, and returns a new Span with the given `operationName`
// A Span with no SpanReference options (e.g., opentracing.ChildOf() or
// opentracing.FollowsFrom()) becomes the root of its own trace.
func (t *tracer) StartSpan(operationName string, options ...opentracing.StartSpanOption) opentracing.Span {
	var opts opentracing.StartSpanOptions
	for _, o := range options {
		o.Apply(&opts)
	}
	if opts.StartTime.IsZero() {
		opts.StartTime = time.Now().UTC()
	}
	var context *spanContext
	for _, ref := range opts.References {
		if ctx, ok := ref.ReferencedContext.(*spanContext); ok && ref.Type == opentracing.ChildOfRef {
			// found a parent context
			context = ctx
		}
	}
	id := random.Uint64()
	span := &span{
		Name:     operationName,
		Service:  t.config.serviceName,
		Resource: operationName,
		Meta:     map[string]string{},
		Metrics:  map[string]float64{},
		SpanID:   id,
		TraceID:  id,
		ParentID: 0,
		Start:    opts.StartTime.UnixNano(),
		tracer:   t,
	}
	if context != nil {
		// this is a child span
		if context.span == nil {
			// the parent is in another process (e.g. transmitted via HTTP headers)
			span.TraceID = context.traceID
			span.ParentID = context.spanID
		} else {
			// the parent is in the same process
			parent := context.span
			parent.RLock()
			defer parent.RUnlock()

			span.Service = parent.Service
			span.TraceID = parent.TraceID
			span.ParentID = parent.SpanID
			span.parent = parent
			span.context = newSpanContext(span, parent.context)

			if parent.hasSamplingPriority() {
				span.setSamplingPriority(parent.getSamplingPriority())
			}
		}
	}
	if context == nil || context.span == nil {
		// this is either a global root span or a process-level root span
		span.context = newSpanContext(span, nil)
		span.SetTag(ext.Pid, strconv.Itoa(os.Getpid()))
		// TODO(ufoot): introduce distributed sampling here
		t.sample(span)
	}
	// add tags from options
	for k, v := range opts.Tags {
		span.SetTag(k, v)
	}
	// add global tags
	for k, v := range t.config.globalTags {
		span.SetTag(k, v)
	}
	return span
}

// Inject takes the `sm` SpanContext instance and injects it for
// propagation within `carrier`. The actual type of `carrier` depends on
// the value of `format`. Currently supported Injectors are:
// * `TextMap`
// * `HTTPHeaders`
func (t *tracer) Inject(ctx opentracing.SpanContext, format interface{}, carrier interface{}) error {
	switch format {
	case opentracing.TextMap, opentracing.HTTPHeaders:
		return t.config.textMapPropagator.Inject(ctx, carrier)
	case opentracing.Binary:
		return t.config.binaryPropagator.Inject(ctx, carrier)
	}
	return opentracing.ErrUnsupportedFormat
}

// Extract returns a SpanContext instance given `format` and `carrier`.
func (t *tracer) Extract(format interface{}, carrier interface{}) (opentracing.SpanContext, error) {
	switch format {
	case opentracing.TextMap, opentracing.HTTPHeaders:
		return t.config.textMapPropagator.Extract(carrier)
	case opentracing.Binary:
		return t.config.binaryPropagator.Extract(carrier)
	}
	return nil, opentracing.ErrUnsupportedFormat
}

// Stop stops the tracer.
func (t *tracer) stop() {
	done := make(chan struct{})
	t.exitReq <- done
	<-done
}

// SetServiceInfo update the application and application type for the given
// service.
func (t *tracer) setServiceInfo(name, app, appType string) {
	t.pushService(service{
		Name:    name,
		App:     app,
		AppType: appType,
	})
}

func (t *tracer) getTraces() [][]*span {
	traces := make([][]*span, 0, len(t.traceBuffer))

	for {
		select {
		case trace := <-t.traceBuffer:
			traces = append(traces, trace)
		default: // return when there's no more data
			return traces
		}
	}
}

// flushTraces will push any currently buffered traces to the server.
func (t *tracer) flushTraces() {
	traces := t.getTraces()

	if t.config.debug {
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
	if t.config.transport == nil || len(traces) == 0 {
		return
	}

	_, err := t.config.transport.sendTraces(traces)
	if err != nil {
		t.pushErr(err)
		t.pushErr(&errorFlushLostTraces{Nb: len(traces)}) // explicit log messages with nb of lost traces
	}
}

func (t *tracer) updateServices() bool {
	servicesModified := false
	for {
		select {
		case service := <-t.serviceBuffer:
			if s, found := t.services[service.Name]; !found || !s.equals(service) {
				t.services[service.Name] = service
				servicesModified = true
			}
		default: // return when there's no more data
			return servicesModified
		}
	}
}

// flushTraces will push any currently buffered services to the server.
func (t *tracer) flushServices() {
	servicesModified := t.updateServices()

	if !servicesModified {
		return
	}

	_, err := t.config.transport.sendServices(t.services)
	if err != nil {
		t.pushErr(err)
		t.pushErr(&errorFlushLostServices{Nb: len(t.services)}) // explicit log messages with nb of lost services
	}
}

// flushErrs will process log messages that were queued
func (t *tracer) flushErrs() {
	logErrors(t.errorBuffer)
}

func (t *tracer) flush() {
	t.flushTraces()
	t.flushServices()
	t.flushErrs()
}

// ForceFlush forces a flush of data (traces and services) to the agent.
// Flushes are done by a background task on a regular basis, so you never
// need to call this manually, mostly useful for testing and debugging.
func (t *tracer) ForceFlush() {
	done := make(chan struct{})
	t.flushAllReq <- done
	<-done
}

// sampleRateMetricKey is the metric key holding the applied sample rate. Has to be the same as the Agent.
const sampleRateMetricKey = "_sample_rate"

// Sample samples a span with the internal sampler.
func (t *tracer) sample(span *span) {
	sampler := t.config.sampler
	sampled := sampler.Sample(span)
	if sampled {
		if rs, ok := sampler.(RateSampler); ok && rs.Rate() < 1 {
			// for limited rate samplers, make note of the rate
			span.setMetric(sampleRateMetricKey, rs.Rate())
		}
	}
	span.context.sampled = sampled
}

// worker periodically flushes traces and services to the transport.
func (t *tracer) worker() {
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			t.flush()

		case done := <-t.flushAllReq:
			t.flush()
			done <- struct{}{}

		case <-t.flushTracesReq:
			t.flushTraces()

		case <-t.flushServicesReq:
			t.flushServices()

		case <-t.flushErrorsReq:
			t.flushErrs()

		case done := <-t.exitReq:
			t.flush()
			done <- struct{}{}
			return
		}
	}
}

// SetServiceInfo sets information about the given service. A tracer is expected to
// be active, which has been started by Start or assigned by opentracing.SetGlobalTracer,
// otherwise it is a no-op.
func SetServiceInfo(name, app, appType string) {
	t, ok := opentracing.GlobalTracer().(*tracer)
	if !ok {
		return
	}
	t.setServiceInfo(name, app, appType)
}

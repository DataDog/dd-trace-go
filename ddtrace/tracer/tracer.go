package tracer

import (
	"log"
	"os"
	"strconv"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v0/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v0/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v0/ddtrace/internal"
)

var _ ddtrace.Tracer = (*tracer)(nil)

// tracer creates, buffers and submits Spans which are used to time blocks of
// computation.
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

// Start starts the tracer with the given set of options.
func Start(opts ...StartOption) {
	if internal.Testing {
		return // mock tracer active
	}
	internal.GlobalTracer = newTracer(opts...)
}

// Stop stops the started tracer. Subsequent calls are valid but become no-op.
func Stop() {
	internal.GlobalTracer.Stop()
	internal.GlobalTracer = &internal.NoopTracer{}
}

// Span is an alias for ddtrace.Span. It is here to allow godoc to group methods returning
// ddtrace.Span. It is recommended to refer to this type as ddtrace.Span instead.
type Span = ddtrace.Span

// StartSpan starts a new span with the given operation name and set of options.
// If the tracer is not started calling this function is a no-op.
func StartSpan(operationName string, opts ...StartSpanOption) Span {
	return internal.GlobalTracer.StartSpan(operationName, opts...)
}

// SetServiceInfo sets information about a service.
// If the tracer is not started calling this function is a no-op.
func SetServiceInfo(name, app, appType string) {
	internal.GlobalTracer.SetServiceInfo(name, app, appType)
}

// Extract extracts a SpanContext from the passed carrier. The carrier is expected
// to implement TextMapReader, otherwise an error is returned.
// If the tracer is not started calling this function is a no-op.
func Extract(carrier interface{}) (ddtrace.SpanContext, error) {
	return internal.GlobalTracer.Extract(carrier)
}

// Inject injects the given SpanContext into the carrier. The carrier is expected to
// implement TextMapWriter, otherwise an error is returned.
// If the tracer is not started calling this function is a no-op.
func Inject(ctx ddtrace.SpanContext, carrier interface{}) error {
	return internal.GlobalTracer.Inject(ctx, carrier)
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

func newTracer(opts ...StartOption) *tracer {
	c := new(config)
	defaults(c)
	for _, fn := range opts {
		fn(c)
	}
	if c.transport == nil {
		c.transport = newTransport(c.agentAddr)
	}
	if c.propagator == nil {
		c.propagator = NewPropagator("", "", "")
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

// flushInterval is the interval at which the buffer contents will be flushed
// to the transport.
const flushInterval = 2 * time.Second

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

// pushTrace pushes a new trace onto the trace buffer. If the trace buffer is getting
// full, it also triggers a flush request.
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
		t.pushErr(&errBufferFull{name: "trace channel", size: len(t.traceBuffer)})
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
		t.pushErr(&errBufferFull{name: "service channel", size: len(t.serviceBuffer)})
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

// StartSpan creates, starts, and returns a new Span with the given `operationName`.
func (t *tracer) StartSpan(operationName string, options ...ddtrace.StartSpanOption) ddtrace.Span {
	var opts ddtrace.StartSpanConfig
	for _, fn := range options {
		fn(&opts)
	}
	var startTime int64
	if opts.StartTime.IsZero() {
		startTime = now()
	} else {
		startTime = opts.StartTime.UnixNano()
	}
	var context *spanContext
	if opts.Parent != nil {
		if ctx, ok := opts.Parent.(*spanContext); ok {
			context = ctx
		}
	}
	id := random.Uint64()
	// span defaults
	span := &span{
		Name:     operationName,
		Service:  t.config.serviceName,
		Resource: operationName,
		Meta:     map[string]string{},
		Metrics:  map[string]float64{},
		SpanID:   id,
		TraceID:  id,
		ParentID: 0,
		Start:    startTime,
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

			if v, ok := parent.Metrics[samplingPriorityKey]; ok {
				span.Metrics[samplingPriorityKey] = v
			}
		}
	}
	if context == nil || context.span == nil {
		// this is either a global root span or a process-level root span
		span.context = newSpanContext(span, nil)
		span.SetTag(ext.Pid, strconv.Itoa(os.Getpid()))
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

// Stop stops the tracer.
func (t *tracer) Stop() {
	done := make(chan struct{})
	t.exitReq <- done
	<-done
}

// SetServiceInfo update the application and application type for the given
// service.
func (t *tracer) SetServiceInfo(name, app, appType string) {
	t.pushService(service{
		Name:    name,
		App:     app,
		AppType: appType,
	})
}

// Inject uses the configured or default TextMap Propagator.
func (t *tracer) Inject(ctx ddtrace.SpanContext, carrier interface{}) error {
	return t.config.propagator.Inject(ctx, carrier)
}

// Extract uses the configured or default TextMap Propagator.
func (t *tracer) Extract(carrier interface{}) (ddtrace.SpanContext, error) {
	return t.config.propagator.Extract(carrier)
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
	if t.config.transport == nil || len(traces) == 0 {
		// nothing to do
		return
	}
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
	_, err := t.config.transport.sendTraces(traces)
	if err != nil {
		t.pushErr(&errLostData{name: "traces", context: err, count: len(traces)})
	}
}

func (t *tracer) updateServices() bool {
	changed := false
	for {
		select {
		case service := <-t.serviceBuffer:
			if s, found := t.services[service.Name]; !found || !s.equals(service) {
				t.services[service.Name] = service
				changed = true
			}
		default: // return when there's no more data
			return changed
		}
	}
}

// flushTraces will push any currently buffered services to the server.
func (t *tracer) flushServices() {
	if !t.updateServices() {
		// services haven't changed
		return
	}
	_, err := t.config.transport.sendServices(t.services)
	if err != nil {
		t.pushErr(&errLostData{name: "services", context: err, count: len(t.services)})
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

// forceFlush forces a flush of data (traces and services) to the agent.
// Flushes are done by a background task on a regular basis, so you never
// need to call this manually, mostly useful for testing and debugging.
func (t *tracer) forceFlush() {
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
	span.context.sampled = sampled
	if !sampled {
		return
	}
	if rs, ok := sampler.(RateSampler); ok && rs.Rate() < 1 {
		// the span was sampled using a rate sampler which wasn't all permissive,
		// so we make note of the sampling rate.
		span.Lock()
		defer span.Unlock()
		if span.finished {
			// we don't touch finished span as they might be flushing
			return
		}
		span.Metrics[sampleRateMetricKey] = rs.Rate()
	}
}

// service holds context information about a given service.
type service struct {
	Name    string `json:"-"`        // the internal of the service (e.g. acme_search, datadog_web)
	App     string `json:"app"`      // the name of the application (e.g. rails, postgres, custom-app)
	AppType string `json:"app_type"` // the type of the application (e.g. db, web)
}

// equals will return true if two services are identical.
func (s service) equals(s2 service) bool {
	return s.Name == s2.Name && s.App == s2.App && s.AppType == s2.AppType
}

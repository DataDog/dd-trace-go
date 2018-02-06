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

var _ opentracing.Tracer = (*Tracer)(nil)

// Tracer creates, buffers and submits Spans which are used to time blocks of
// compuration.
//
// When a tracer is disabled, it will not submit spans for processing.
type Tracer struct {
	*config

	services map[string]service // name -> service

	traceBuffer   chan []*span
	serviceBuffer chan service
	errorBuffer   chan error

	flushAllReq      chan chan<- struct{}
	flushTracesReq   chan struct{}
	flushServicesReq chan struct{}
	flushErrorsReq   chan struct{}

	exitReq chan chan<- struct{}
}

func New(opts ...Option) *Tracer {
	c := new(config)
	defaults(c)
	for _, fn := range opts {
		fn(c)
	}
	if c.transport == nil {
		c.transport = newTransport(c.agentAddr)
	}
	t := &Tracer{
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

func (t *Tracer) pushTrace(trace []*span) {
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

func (t *Tracer) pushService(service service) {
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

func (t *Tracer) pushErr(err error) {
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
func (t *Tracer) StartSpan(operationName string, options ...opentracing.StartSpanOption) opentracing.Span {
	sso := opentracing.StartSpanOptions{}
	for _, o := range options {
		o.Apply(&sso)
	}
	return t.startSpanWithOptions(operationName, sso)
}

func (t *Tracer) startSpanWithOptions(operationName string, options opentracing.StartSpanOptions) opentracing.Span {
	if options.StartTime.IsZero() {
		options.StartTime = time.Now().UTC()
	}

	context := new(spanContext)
	var hasParent bool
	var parent, span *span

	for _, ref := range options.References {
		ctx, ok := ref.ReferencedContext.(*spanContext)
		if !ok {
			// ignore the spanContext since it's not valid
			continue
		}

		// if we have parenting define it
		if ref.Type == opentracing.ChildOfRef {
			hasParent = true
			context = ctx
			parent = ctx.span
		}
	}

	if parent == nil {
		// create a root Span with the default service name and resource
		span = t.newRootSpan(operationName, t.config.serviceName, operationName)

		if hasParent {
			// the Context doesn't have a Span reference because it
			// has been propagated from another process, so we set these
			// values manually
			span.TraceID = context.traceID
			span.ParentID = context.spanID
			t.sample(span)
		}
	} else {
		// create a child Span that inherits from a parent
		span = t.newChildSpan(operationName, parent)
	}

	// create an OpenTracing compatible Span; the SpanContext has a
	// back-reference that is used for parent-child hierarchy
	span.context = &spanContext{
		traceID:  span.TraceID,
		spanID:   span.SpanID,
		parentID: span.ParentID,
		sampled:  span.Sampled,
		span:     span,
	}
	span.Start = options.StartTime.UnixNano()
	span.tracer = t

	if parent != nil {
		// propagate baggage items
		if l := len(parent.context.baggage); l > 0 {
			span.context.baggage = make(map[string]string, len(parent.context.baggage))
			for k, v := range parent.context.baggage {
				span.context.baggage[k] = v
			}
		}
	}

	// add tags from options
	for k, v := range options.Tags {
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
func (t *Tracer) Inject(ctx opentracing.SpanContext, format interface{}, carrier interface{}) error {
	switch format {
	case opentracing.TextMap, opentracing.HTTPHeaders:
		return t.config.textMapPropagator.Inject(ctx, carrier)
	case opentracing.Binary:
		return t.config.binaryPropagator.Inject(ctx, carrier)
	}
	return opentracing.ErrUnsupportedFormat
}

// Extract returns a SpanContext instance given `format` and `carrier`.
func (t *Tracer) Extract(format interface{}, carrier interface{}) (opentracing.SpanContext, error) {
	switch format {
	case opentracing.TextMap, opentracing.HTTPHeaders:
		return t.config.textMapPropagator.Extract(carrier)
	case opentracing.Binary:
		return t.config.binaryPropagator.Extract(carrier)
	}
	return nil, opentracing.ErrUnsupportedFormat
}

// Stop stops the tracer.
func (t *Tracer) Stop() {
	done := make(chan struct{})
	t.exitReq <- done
	<-done
}

// SetServiceInfo update the application and application type for the given
// service.
func (t *Tracer) setServiceInfo(name, app, appType string) {
	t.pushService(service{
		Name:    name,
		App:     app,
		AppType: appType,
	})
}

// newRootSpan creates a span with no parent. Its ids will be randomly
// assigned.
func (t *Tracer) newRootSpan(name, service, resource string) *span {
	id := random.Uint64()

	span := newSpan(name, service, resource, id, id, 0, t)
	span.buffer = newSpanBuffer(t, 0, 0)
	span.buffer.Push(span)
	span.SetTag(ext.Pid, strconv.Itoa(os.Getpid()))

	// TODO(ufoot): introduce distributed sampling here
	t.sample(span)

	return span
}

// newChildSpan returns a new span that is child of the Span passed as
// argument.
func (t *Tracer) newChildSpan(name string, parent *span) *span {
	if parent == nil {
		// don't crash
		return t.newRootSpan(name, t.config.serviceName, name)
	}

	parent.RLock()
	defer parent.RUnlock()

	id := random.Uint64()
	span := newSpan(name, parent.Service, name, id, parent.TraceID, parent.SpanID, parent.tracer)
	span.Sampled = parent.Sampled

	if parent.hasSamplingPriority() {
		span.setSamplingPriority(parent.getSamplingPriority())
	}

	span.parent = parent
	span.buffer = parent.buffer
	span.buffer.Push(span)

	return span
}

func (t *Tracer) getTraces() [][]*span {
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
func (t *Tracer) flushTraces() {
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

func (t *Tracer) updateServices() bool {
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
func (t *Tracer) flushServices() {
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
func (t *Tracer) flushErrs() {
	logErrors(t.errorBuffer)
}

func (t *Tracer) flush() {
	t.flushTraces()
	t.flushServices()
	t.flushErrs()
}

// ForceFlush forces a flush of data (traces and services) to the agent.
// Flushes are done by a background task on a regular basis, so you never
// need to call this manually, mostly useful for testing and debugging.
func (t *Tracer) ForceFlush() {
	done := make(chan struct{})
	t.flushAllReq <- done
	<-done
}

// Sample samples a span with the internal sampler.
func (t *Tracer) sample(span *span) {
	t.config.sampler.Sample(span)
}

// worker periodically flushes traces and services to the transport.
func (t *Tracer) worker() {
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

func SetServiceInfo(name, app, appType string) {
	t, ok := opentracing.GlobalTracer().(*Tracer)
	if !ok {
		return
	}
	t.setServiceInfo(name, app, appType)
}

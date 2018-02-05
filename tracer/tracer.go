package tracer

import (
	"log"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/DataDog/dd-trace-go/tracer/ext"
	"github.com/opentracing/opentracing-go"
)

const flushInterval = 2 * time.Second

type Service struct {
	Name    string `json:"-"`        // the internal of the service (e.g. acme_search, datadog_web)
	App     string `json:"app"`      // the name of the application (e.g. rails, postgres, custom-app)
	AppType string `json:"app_type"` // the type of the application (e.g. db, web)
}

func (s Service) Equal(s2 Service) bool {
	return s.Name == s2.Name && s.App == s2.App && s.AppType == s2.AppType
}

var _ opentracing.Tracer = (*Tracer)(nil)

// Tracer creates, buffers and submits Spans which are used to time blocks of
// compuration.
//
// When a tracer is disabled, it will not submit spans for processing.
type Tracer struct {
	*config

	meta   map[string]string
	metaMu sync.RWMutex

	channels tracerChans
	services map[string]Service // name -> service

	exit   chan struct{}
	exitWG *sync.WaitGroup

	forceFlushIn  chan struct{}
	forceFlushOut chan struct{}
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
		config:        c,
		channels:      newTracerChans(),
		services:      make(map[string]Service),
		exit:          make(chan struct{}),
		exitWG:        &sync.WaitGroup{},
		forceFlushIn:  make(chan struct{}),
		forceFlushOut: make(chan struct{}),
	}

	t.exitWG.Add(1)
	go t.worker()

	return t
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
	var parent, span *Span

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
			t.Sample(span)
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
	close(t.exit)
	t.exitWG.Wait()
}

// SetServiceInfo update the application and application type for the given
// service.
func (t *Tracer) SetServiceInfo(name, app, appType string) {
	t.channels.pushService(Service{
		Name:    name,
		App:     app,
		AppType: appType,
	})
}

// newRootSpan creates a span with no parent. Its ids will be randomly
// assigned.
func (t *Tracer) newRootSpan(name, service, resource string) *Span {
	id := random.Uint64()

	span := newSpan(name, service, resource, id, id, 0, t)
	span.buffer = newSpanBuffer(t.channels, 0, 0)
	span.buffer.Push(span)
	span.SetMeta(ext.Pid, strconv.Itoa(os.Getpid()))

	// TODO(ufoot): introduce distributed sampling here
	t.Sample(span)

	return span
}

// newChildSpan returns a new span that is child of the Span passed as
// argument.
func (t *Tracer) newChildSpan(name string, parent *Span) *Span {
	if parent == nil {
		// don't crash
		return t.newRootSpan(name, t.config.serviceName, name)
	}

	parent.RLock()
	defer parent.RUnlock()

	id := random.Uint64()
	span := newSpan(name, parent.Service, name, id, parent.TraceID, parent.SpanID, parent.tracer)
	span.Sampled = parent.Sampled

	if parent.HasSamplingPriority() {
		span.SetSamplingPriority(parent.GetSamplingPriority())
	}

	span.parent = parent
	span.buffer = parent.buffer
	span.buffer.Push(span)

	return span
}

func (t *Tracer) getTraces() [][]*Span {
	traces := make([][]*Span, 0, len(t.channels.trace))

	for {
		select {
		case trace := <-t.channels.trace:
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
		t.channels.pushErr(err)
		t.channels.pushErr(&errorFlushLostTraces{Nb: len(traces)}) // explicit log messages with nb of lost traces
	}
}

func (t *Tracer) updateServices() bool {
	servicesModified := false
	for {
		select {
		case service := <-t.channels.service:
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
func (t *Tracer) flushServices() {
	servicesModified := t.updateServices()

	if !servicesModified {
		return
	}

	_, err := t.config.transport.sendServices(t.services)
	if err != nil {
		t.channels.pushErr(err)
		t.channels.pushErr(&errorFlushLostServices{Nb: len(t.services)}) // explicit log messages with nb of lost services
	}
}

// flushErrs will process log messages that were queued
func (t *Tracer) flushErrs() {
	logErrors(t.channels.err)
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
	t.forceFlushIn <- struct{}{}
	<-t.forceFlushOut
}

// Sample samples a span with the internal sampler.
func (t *Tracer) Sample(span *Span) {
	t.config.sampler.Sample(span)
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
			t.forceFlushOut <- struct{}{} // caller blocked until this is done

		case <-t.channels.traceFlush:
			t.flushTraces()

		case <-t.channels.serviceFlush:
			t.flushServices()

		case <-t.channels.errFlush:
			t.flushErrs()

		case <-t.exit:
			t.flush()
			return
		}
	}
}

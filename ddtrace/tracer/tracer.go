package tracer

import (
	"os"
	"strconv"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
)

var _ ddtrace.Tracer = (*tracer)(nil)

// tracer creates, buffers and submits Spans which are used to time blocks of
// computation. They are accumulated and streamed into an internal payload,
// which is flushed to the agent whenever its size exceeds a specific threshold
// or when a certain interval of time has passed, whichever happens first.
//
// tracer operates based on a worker loop which responds to various request
// channels. It additionally holds two buffers which accumulates error and trace
// queues to be processed by the payload encoder.
type tracer struct {
	*config
}

// Start starts the tracer with the given set of options. It will stop and replace
// any running tracer, meaning that calling it several times will result in a restart
// of the tracer by replacing the current instance with a new one.
func Start(opts ...StartOption) {
	if internal.Testing {
		return // mock tracer active
	}
	internal.SetGlobalTracer(newTracer(opts...))
}

// Stop stops the started tracer. Subsequent calls are valid but become no-op.
func Stop() {
	internal.SetGlobalTracer(&internal.NoopTracer{})
}

// Span is an alias for ddtrace.Span. It is here to allow godoc to group methods returning
// ddtrace.Span. It is recommended and is considered more correct to refer to this type as
// ddtrace.Span instead.
type Span = ddtrace.Span

// StartSpan starts a new span with the given operation name and set of options.
// If the tracer is not started, calling this function is a no-op.
func StartSpan(operationName string, opts ...StartSpanOption) Span {
	return internal.GetGlobalTracer().StartSpan(operationName, opts...)
}

// Extract extracts a SpanContext from the carrier. The carrier is expected
// to implement TextMapReader, otherwise an error is returned.
// If the tracer is not started, calling this function is a no-op.
func Extract(carrier interface{}) (ddtrace.SpanContext, error) {
	return internal.GetGlobalTracer().Extract(carrier)
}

// Inject injects the given SpanContext into the carrier. The carrier is
// expected to implement TextMapWriter, otherwise an error is returned.
// If the tracer is not started, calling this function is a no-op.
func Inject(ctx ddtrace.SpanContext, carrier interface{}) error {
	return internal.GetGlobalTracer().Inject(ctx, carrier)
}

func newTracer(opts ...StartOption) *tracer {
	c := new(config)
	defaults(c)
	for _, fn := range opts {
		fn(c)
	}
	if c.propagator == nil {
		c.propagator = NewPropagator(nil)
	}
	if c.exporter == nil {
		c.exporter = newDefaultExporter(c.agentAddr)
	}
	t := &tracer{config: c}
	return t
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
	}
	if context != nil {
		// this is a child span
		span.TraceID = context.traceID
		span.ParentID = context.spanID
		if context.hasSamplingPriority() {
			span.Metrics[samplingPriorityKey] = float64(context.samplingPriority())
		}
		if context.span != nil {
			context.span.RLock()
			span.Service = context.span.Service
			context.span.RUnlock()
		}
	}
	span.context = newSpanContext(span, context)
	if context == nil || context.span == nil {
		// root span (global or process-level)
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
	if v, ok := t.config.exporter.(interface {
		Flush()
	}); ok {
		v.Flush()
	}
}

// Inject uses the configured or default TextMap Propagator.
func (t *tracer) Inject(ctx ddtrace.SpanContext, carrier interface{}) error {
	return t.config.propagator.Inject(ctx, carrier)
}

// Extract uses the configured or default TextMap Propagator.
func (t *tracer) Extract(carrier interface{}) (ddtrace.SpanContext, error) {
	return t.config.propagator.Extract(carrier)
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
		span.Metrics[sampleRateMetricKey] = rs.Rate()
		span.Unlock()
	}
}

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package tracer

import (
	"os"
	"strconv"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	"github.com/DataDog/datadog-go/statsd"
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
	*payload

	// flushChan triggers a flush of the buffered payload. If the sent channel is
	// not nil (only in tests), it will receive confirmation of a finished flush.
	flushChan chan chan<- struct{}

	// exitChan requests that the tracer stops.
	exitChan chan struct{}

	// payloadChan receives traces to be added to the payload.
	payloadChan chan []*span

	// stopped is a channel that will be closed when the worker has exited.
	stopped chan struct{}

	// syncPush is used for testing. When non-nil, it causes pushTrace to become
	// a synchronous (blocking) operation, meaning that it will only return after
	// the trace has been fully processed and added onto the payload.
	syncPush chan struct{}

	// prioritySampling holds an instance of the priority sampler.
	prioritySampling *prioritySampler

	// pid of the process
	pid string

	// statsd client for tracer metrics.
	statsd *statsd.Client
}

const (
	// flushInterval is the interval at which the payload contents will be flushed
	// to the transport.
	flushInterval = 2 * time.Second

	// payloadMaxLimit is the maximum payload size allowed and should indicate the
	// maximum size of the package that the agent can receive.
	payloadMaxLimit = 9.5 * 1024 * 1024 // 9.5 MB

	// payloadSizeLimit specifies the maximum allowed size of the payload before
	// it will trigger a flush to the transport.
	payloadSizeLimit = payloadMaxLimit / 2
)

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
	log.Flush()
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

// payloadQueueSize is the buffer size of the trace channel.
const payloadQueueSize = 1000

func newTracer(opts ...StartOption) *tracer {
	c := new(config)
	defaults(c)
	for _, fn := range opts {
		fn(c)
	}
	if c.transport == nil {
		c.transport = newTransport(c.agentAddr, c.httpRoundTripper)
	}
	if c.propagator == nil {
		c.propagator = NewPropagator(nil)
	}
	if c.logger != nil {
		log.UseLogger(c.logger)
	}
	if c.debug {
		log.SetLevel(log.LevelDebug)
	}

	statsd, err := statsd.NewBuffered(c.dogstatsdAddr, 40)
	if err != nil {
		log.Warn("Runtime and tracer metrics disabled: %v", err)
		statsd = nil
	}
	t := &tracer{
		config:           c,
		payload:          newPayload(),
		flushChan:        make(chan chan<- struct{}),
		exitChan:         make(chan struct{}),
		payloadChan:      make(chan []*span, payloadQueueSize),
		stopped:          make(chan struct{}),
		prioritySampling: newPrioritySampler(),
		pid:              strconv.Itoa(os.Getpid()),
		statsd:           statsd,
	}
	if t.statsd != nil {
		t.statsd.Incr("datadog.tracer.started", t.statsTags(), 1)
		if c.runtimeMetrics {
			log.Debug("Runtime metrics enabled.")
			go t.reportMetrics(statsd, defaultMetricsReportInterval)
		}
	}
	go t.worker()
	return t
}

func (t *tracer) statsTags() []string {
	var tags []string
	if t.config.serviceName != "" {
		tags = append(tags, "service:"+t.config.serviceName)
	}
	if t.config.hostname != "" {
		tags = append(tags, "host:"+t.config.hostname)
	}
	if v, ok := t.config.globalTags[ext.Environment]; ok {
		if vv, ok := v.(string); ok {
			tags = append(tags, "env:"+vv)
		}
	}
	return tags
}

// worker receives finished traces to be added into the payload, as well
// as periodically flushes traces to the transport.
func (t *tracer) worker() {
	defer close(t.stopped)
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	for {
		select {
		case trace := <-t.payloadChan:
			t.pushPayload(trace)

		case <-ticker.C:
			if t.statsd != nil {
				tags := t.statsTags()
				tags = append(tags, "reason:timeout")
				t.statsd.Incr("datadog.tracer.flush.count", tags, 1)
			}
			t.flushPayload()

		case confirm := <-t.flushChan:
			if t.statsd != nil {
				tags := t.statsTags()
				tags = append(tags, "reason:payload-full")
				t.statsd.Incr("datadog.tracer.flush.count", tags, 1)
			}
			t.flushPayload()
			if confirm != nil {
				confirm <- struct{}{}
			}

		case <-t.exitChan:
		loop:
			// the loop ensures that the payload channel is fully drained
			// before the final flush to ensure no traces are lost (see #526)
			for {
				select {
				case trace := <-t.payloadChan:
					t.pushPayload(trace)
				default:
					break loop
				}
			}
			t.flushPayload()
			return
		}
	}
}

func (t *tracer) pushTrace(trace []*span) {
	select {
	case <-t.stopped:
		return
	default:
	}
	select {
	case t.payloadChan <- trace:
	default:
		log.Error("payload queue full, dropping %d traces", len(trace))
	}
	if t.syncPush != nil {
		// only in tests
		<-t.syncPush
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
	id := opts.SpanID
	if id == 0 {
		id = random.Uint64()
	}
	// span defaults
	span := &span{
		Name:     operationName,
		Service:  t.config.serviceName,
		Resource: operationName,
		SpanID:   id,
		TraceID:  id,
		Start:    startTime,
		taskEnd:  startExecutionTracerTask(operationName),
	}
	if context != nil {
		// this is a child span
		span.TraceID = context.traceID
		span.ParentID = context.spanID
		if context.hasSamplingPriority() {
			span.setMetric(keySamplingPriority, float64(context.samplingPriority()))
		}
		if context.span != nil {
			// local parent, inherit service
			context.span.RLock()
			span.Service = context.span.Service
			context.span.RUnlock()
		} else {
			// remote parent
			if context.origin != "" {
				// mark origin
				span.setMeta(keyOrigin, context.origin)
			}
		}
	}
	span.context = newSpanContext(span, context)
	if context == nil || context.span == nil {
		// this is either a root span or it has a remote parent, we should add the PID.
		span.setMeta(ext.Pid, t.pid)
		if t.hostname != "" {
			span.setMeta(keyHostname, t.hostname)
		}
		if _, ok := opts.Tags[ext.ServiceName]; !ok && t.config.runtimeMetrics {
			// this is a root span in the global service; runtime metrics should
			// be linked to it:
			span.setMeta("language", "go")
		}
	}
	// add tags from options
	for k, v := range opts.Tags {
		span.SetTag(k, v)
	}
	// add global tags
	for k, v := range t.config.globalTags {
		span.SetTag(k, v)
	}
	if context == nil {
		// this is a brand new trace, sample it
		t.sample(span)
	}
	return span
}

// Stop stops the tracer.
func (t *tracer) Stop() {
	select {
	case <-t.stopped:
		return
	default:
		if t.statsd != nil {
			t.statsd.Incr("datadog.tracer.stopped", t.statsTags(), 1)
			t.statsd.Close()
			t.statsd = nil
		}
		t.exitChan <- struct{}{}
		<-t.stopped
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

// flush will push any currently buffered traces to the server.
func (t *tracer) flushPayload() {
	if t.statsd != nil {
		start := time.Now()
		tags := t.statsTags()
		t.statsd.Gauge("datadog.tracer.flush.bytes", float64(t.payload.size()), tags, 1)
		t.statsd.Gauge("datadog.tracer.flush.traces", float64(t.payload.itemCount()), tags, 1)
		defer func() { t.statsd.Timing("datadog.tracer.flush.duration", time.Since(start), tags, 1) }()
	}
	if t.payload.itemCount() == 0 {
		return
	}
	size, count := t.payload.size(), t.payload.itemCount()
	log.Debug("Sending payload: size: %d traces: %d\n", size, count)
	rc, err := t.config.transport.send(t.payload)
	if err != nil {
		log.Error("lost %d traces: %v", count, err)
	}
	if err == nil {
		t.prioritySampling.readRatesJSON(rc) // TODO: handle error?
	}
	t.payload.reset()
}

// pushPayload pushes the trace onto the payload. If the payload becomes
// larger than the threshold as a result, it sends a flush request.
func (t *tracer) pushPayload(trace []*span) {
	if err := t.payload.push(trace); err != nil {
		if t.statsd != nil {
			t.statsd.Incr("datadog.tracer.payload.error", t.statsTags(), 1)
		}
		log.Error("error encoding msgpack: %v", err)
	}
	if t.payload.size() > payloadSizeLimit {
		// getting large
		select {
		case t.flushChan <- nil:
		default:
			// flush already queued
		}
	}
	if t.syncPush != nil {
		// only in tests
		t.syncPush <- struct{}{}
	}
}

// sampleRateMetricKey is the metric key holding the applied sample rate. Has to be the same as the Agent.
const sampleRateMetricKey = "_sample_rate"

// Sample samples a span with the internal sampler.
func (t *tracer) sample(span *span) {
	if span.context.hasSamplingPriority() {
		// sampling decision was already made
		return
	}
	sampler := t.config.sampler
	if !sampler.Sample(span) {
		span.context.drop = true
		return
	}
	if rs, ok := sampler.(RateSampler); ok && rs.Rate() < 1 {
		span.setMetric(sampleRateMetricKey, rs.Rate())
	}
	t.prioritySampling.apply(span)
}

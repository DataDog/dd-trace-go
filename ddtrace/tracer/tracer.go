// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	gocontext "context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime/pprof"
	"strconv"
	"sync"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
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
	config *config

	// features holds the capabilities of the agent and determines some
	// of the behaviour of the tracer.
	features *agentFeatures

	// stats specifies the concentrator used to compute statistics, when client-side
	// stats are enabled.
	stats *concentrator

	// traceWriter is responsible for sending finished traces to their
	// destination, such as the Trace Agent or Datadog Forwarder.
	traceWriter traceWriter

	// out receives traces to be added to the payload.
	out chan []*span

	// flush receives a channel onto which it will confirm after a flush has been
	// triggered and completed.
	flush chan chan<- struct{}

	// stop causes the tracer to shut down when closed.
	stop chan struct{}

	// stopOnce ensures the tracer is stopped exactly once.
	stopOnce sync.Once

	// wg waits for all goroutines to exit when stopping.
	wg sync.WaitGroup

	// prioritySampling holds an instance of the priority sampler.
	prioritySampling *prioritySampler

	// pid of the process
	pid string

	// These integers track metrics about spans and traces as they are started,
	// finished, and dropped
	spansStarted, spansFinished, tracesDropped int64

	// Records the number of dropped P0 traces and spans.
	droppedP0Traces, droppedP0Spans uint64

	// rulesSampling holds an instance of the rules sampler. These are user-defined
	// rules for applying a sampling rate to spans that match the designated service
	// or operation name.
	rulesSampling *rulesSampler
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

	// concurrentConnectionLimit specifies the maximum number of concurrent outgoing
	// connections allowed.
	concurrentConnectionLimit = 100
)

// statsInterval is the interval at which health metrics will be sent with the
// statsd client; replaced in tests.
var statsInterval = 10 * time.Second

// Start starts the tracer with the given set of options. It will stop and replace
// any running tracer, meaning that calling it several times will result in a restart
// of the tracer by replacing the current instance with a new one.
func Start(opts ...StartOption) {
	if internal.Testing {
		return // mock tracer active
	}
	t := newTracer(opts...)
	if t.config.HasFeature("discovery") {
		t.loadAgentFeatures()
	}
	internal.SetGlobalTracer(t)
	if t.config.logStartup {
		logStartup(t)
	}
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

func newUnstartedTracer(opts ...StartOption) *tracer {
	c := newConfig(opts...)
	envRules, err := samplingRulesFromEnv()
	if err != nil {
		log.Warn("DIAGNOSTICS Error(s) parsing DD_TRACE_SAMPLING_RULES: %s", err)
	}
	if envRules != nil {
		c.samplingRules = envRules
	}
	sampler := newPrioritySampler()
	var writer traceWriter
	if c.logToStdout {
		writer = newLogTraceWriter(c)
	} else {
		writer = newAgentTraceWriter(c, sampler)
	}
	t := &tracer{
		config:           c,
		traceWriter:      writer,
		out:              make(chan []*span, payloadQueueSize),
		stop:             make(chan struct{}),
		flush:            make(chan chan<- struct{}),
		rulesSampling:    newRulesSampler(c.samplingRules),
		prioritySampling: sampler,
		pid:              strconv.Itoa(os.Getpid()),
		features:         &agentFeatures{},
		stats:            newConcentrator(c, defaultStatsBucketSize),
	}
	return t
}

func newTracer(opts ...StartOption) *tracer {
	t := newUnstartedTracer(opts...)
	c := t.config
	t.config.statsd.Incr("datadog.tracer.started", nil, 1)
	if c.runtimeMetrics {
		log.Debug("Runtime metrics enabled.")
		t.wg.Add(1)
		go func() {
			defer t.wg.Done()
			t.reportRuntimeMetrics(defaultMetricsReportInterval)
		}()
	}
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		tick := t.config.tickChan
		if tick == nil {
			ticker := time.NewTicker(flushInterval)
			defer ticker.Stop()
			tick = ticker.C
		}
		t.worker(tick)
	}()
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		t.reportHealthMetrics(statsInterval)
	}()
	t.stats.Start()
	return t
}

// Flush flushes any buffered traces. Flush is in effect only if a tracer
// is started. Users do not have to call Flush in order to ensure that
// traces reach Datadog. It is a convenience method dedicated to a specific
// use case described below.
//
// Flush is of use in Lambda environments, where starting and stopping
// the tracer on each invokation may create too much latency. In this
// scenario, a tracer may be started and stopped by the parent process
// whereas the invokation can make use of Flush to ensure any created spans
// reach the agent.
func Flush() {
	if t, ok := internal.GetGlobalTracer().(*tracer); ok {
		t.flushSync()
	}
}

// flushSync triggers a flush and waits for it to complete.
func (t *tracer) flushSync() {
	done := make(chan struct{})
	t.flush <- done
	<-done
}

// agentFeatures holds information about the trace-agent's capabilities.
type agentFeatures struct {
	mu sync.RWMutex

	// DropP0s reports whether it's ok for the tracer to not send any
	// P0 traces to the agent.
	DropP0s bool

	// V05 reports whether it's ok to use the /v0.5/traces endpoint format.
	V05 bool // TODO(x): Not yet implemented

	// Stats reports whether the agent can receive client-computed stats on
	// the /v0.6/stats endpoint.
	Stats bool
}

// Load returns the current features.
func (f *agentFeatures) Load() agentFeatures {
	f.mu.RLock()
	out := *f
	f.mu.RUnlock()
	return out
}

// Store stores the new features.
func (f *agentFeatures) Store(newf agentFeatures) {
	f.mu.Lock()
	f.DropP0s = newf.DropP0s
	f.V05 = newf.V05
	f.Stats = newf.Stats
	f.mu.Unlock()
}

// loadAgentFeatures queries the trace-agent for its capabilities and updates
// the tracer's behaviour.
func (t *tracer) loadAgentFeatures() {
	if t.config.logToStdout {
		// there is no agent
		return
	}
	resp, err := http.Get(fmt.Sprintf("http://%s/info", t.config.agentAddr))
	if err != nil {
		log.Error("Loading features: %v", err)
		return
	}
	if resp.StatusCode == http.StatusNotFound {
		// agent is older than 7.28.0, features not discoverable
		t.features.Store(agentFeatures{})
		return
	}
	defer resp.Body.Close()
	type infoResponse struct {
		Endpoints     []string `json:"endpoints"`
		ClientDropP0s bool     `json:"client_drop_p0s"`
	}
	var info infoResponse
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		log.Error("Decoding features: %v", err)
		return
	}
	f := agentFeatures{DropP0s: info.ClientDropP0s}
	for _, endpoint := range info.Endpoints {
		switch endpoint {
		case "/v0.6/stats":
			f.Stats = true
		case "/v0.5/traces":
			f.V05 = true
		}
	}
	t.features.Store(f)
}

// worker receives finished traces to be added into the payload, as well
// as periodically flushes traces to the transport.
func (t *tracer) worker(tick <-chan time.Time) {
	for {
		select {
		case trace := <-t.out:
			t.traceWriter.add(trace)

		case <-tick:
			t.config.statsd.Incr("datadog.tracer.flush_triggered", []string{"reason:scheduled"}, 1)
			t.traceWriter.flush()

		case done := <-t.flush:
			t.config.statsd.Incr("datadog.tracer.flush_triggered", []string{"reason:invoked"}, 1)
			t.traceWriter.flush()
			// TODO(x): In reality, the traceWriter.flush() call is not synchronous
			// when using the agent traceWriter. However, this functionnality is used
			// in Lambda so for that purpose this mechanism should suffice.
			done <- struct{}{}

		case <-t.stop:
		loop:
			// the loop ensures that the payload channel is fully drained
			// before the final flush to ensure no traces are lost (see #526)
			for {
				select {
				case trace := <-t.out:
					t.traceWriter.add(trace)
				default:
					break loop
				}
			}
			return
		}
	}
}

func (t *tracer) pushTrace(trace []*span) {
	select {
	case <-t.stop:
		return
	default:
	}
	select {
	case t.out <- trace:
	default:
		log.Error("payload queue full, dropping %d traces", len(trace))
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
		Name:         operationName,
		Service:      t.config.serviceName,
		Resource:     operationName,
		SpanID:       id,
		TraceID:      id,
		Start:        startTime,
		taskEnd:      startExecutionTracerTask(operationName),
		noDebugStack: t.config.noDebugStack,
	}
	if t.config.hostname != "" {
		span.setMeta(keyHostname, t.config.hostname)
	}
	if context != nil {
		// this is a child span
		span.TraceID = context.traceID
		span.ParentID = context.spanID
		if p, ok := context.samplingPriority(); ok {
			span.setMetric(keySamplingPriority, float64(p))
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
	if context == nil || context.span == nil || context.span.Service != span.Service {
		span.setMetric(keyTopLevel, 1)
		// all top level spans are measured. So the measured tag is redundant.
		delete(span.Metrics, keyMeasured)
	}
	if t.config.version != "" && span.Service == t.config.serviceName {
		span.SetTag(ext.Version, t.config.version)
	}
	if t.config.env != "" {
		span.SetTag(ext.Environment, t.config.env)
	}
	if _, ok := span.context.samplingPriority(); !ok {
		// if not already sampled or a brand new trace, sample it
		t.sample(span)
	}
	var labels []string
	if t.config.profilerHotspots {
		labels = append(labels, "span id", fmt.Sprintf("%d", span.SpanID))
	}
	if span.context.trace != nil && span.context.trace.root != nil {
		localRootSpan := span.context.trace.root
		if t.config.profilerHotspots {
			// TODO(fg) should we add "span id" above if this branch doesn't get hit?
			labels = append(labels, "local root span id", fmt.Sprintf("%d", localRootSpan.SpanID))
		}
		if t.config.profilerEndpoints {
			// TODO(fg) this MUST NOT contain personally identifiable information, is
			// it safe to assume that this guarantee will be met here?
			labels = append(labels, "trace endpoint", localRootSpan.Resource)
		}
		if len(labels) > 0 {
			ctx := opts.Context
			if ctx == nil {
				ctx = gocontext.Background()
			} else {
				span.restoreContext = ctx
			}
			span.labelContext = pprof.WithLabels(ctx, pprof.Labels(labels...))
			pprof.SetGoroutineLabels(span.labelContext)
		}
	}
	log.Debug("Started Span: %v, Operation: %s, Resource: %s, Tags: %v, %v", span, span.Name, span.Resource, span.Meta, span.Metrics)
	return span
}

// Stop stops the tracer.
func (t *tracer) Stop() {
	t.stopOnce.Do(func() {
		close(t.stop)
		t.config.statsd.Incr("datadog.tracer.stopped", nil, 1)
	})
	t.stats.Stop()
	t.wg.Wait()
	t.traceWriter.stop()
	t.config.statsd.Close()
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
	if _, ok := span.context.samplingPriority(); ok {
		// sampling decision was already made
		return
	}
	sampler := t.config.sampler
	if !sampler.Sample(span) {
		span.context.trace.drop()
		return
	}
	if rs, ok := sampler.(RateSampler); ok && rs.Rate() < 1 {
		span.setMetric(sampleRateMetricKey, rs.Rate())
	}
	if t.rulesSampling.apply(span) {
		return
	}
	t.prioritySampling.apply(span)
}

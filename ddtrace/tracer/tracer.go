// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"context"
	gocontext "context"
	"encoding/binary"
	"os"
	rt "runtime/trace"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	globalinternal "github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec"
	"github.com/DataDog/dd-trace-go/v2/internal/datastreams"
	"github.com/DataDog/dd-trace-go/v2/internal/hostname"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/remoteconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"

	"github.com/DataDog/datadog-agent/pkg/obfuscate"
)

var _ ddtrace.Tracer = (*Tracer)(nil)

// Tracer creates, buffers and submits Spans which are used to time blocks of
// computation. They are accumulated and streamed into an internal payload,
// which is flushed to the agent whenever its size exceeds a specific threshold
// or when a certain interval of time has passed, whichever happens first.
//
// Tracer operates based on a worker loop which responds to various request
// channels. It additionally holds two buffers which accumulates error and trace
// queues to be processed by the payload encoder.
type Tracer struct {
	config *config

	// stats specifies the concentrator used to compute statistics, when client-side
	// stats are enabled.
	stats *concentrator

	// traceWriter is responsible for sending finished traces to their
	// destination, such as the Trace Agent or Datadog Forwarder.
	traceWriter traceWriter

	// out receives chunk with spans to be added to the payload.
	out chan *ddtrace.Chunk

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
	pid int

	// These integers track metrics about spans and traces as they are started,
	// finished, and dropped
	spansStarted, spansFinished, tracesDropped uint32

	// Records the number of dropped P0 traces and spans.
	droppedP0Traces, droppedP0Spans uint32

	// partialTrace the number of partially dropped traces.
	partialTraces uint32

	// rulesSampling holds an instance of the rules sampler used to apply either trace sampling,
	// or single span sampling rules on spans. These are user-defined
	// rules for applying a sampling rate to spans that match the designated service
	// or operation name.
	rulesSampling *rulesSampler

	// obfuscator holds the obfuscator used to obfuscate resources in aggregated stats.
	// obfuscator may be nil if disabled.
	obfuscator *obfuscate.Obfuscator

	// statsd is used for tracking metrics associated with the runtime and the tracer.
	statsd globalinternal.StatsdClient

	// dataStreams processes data streams monitoring information
	dataStreams *datastreams.Processor

	// abandonedSpansDebugger specifies where and how potentially abandoned spans are stored
	// when abandoned spans debugging is enabled.
	abandonedSpansDebugger *abandonedSpansDebugger
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
	if ddtrace.Testing {
		return // mock tracer active
	}
	defer telemetry.Time(telemetry.NamespaceGeneral, "init_time", nil, true)()
	t := newTracer(opts...)
	if !t.config.enabled {
		// TODO: instrumentation telemetry client won't get started
		// if tracing is disabled, but we still want to capture this
		// telemetry information. Will be fixed when the tracer and profiler
		// share control of the global telemetry client.
		return
	}
	ddtrace.SetGlobalTracer(t)
	if t.config.logStartup {
		logStartup(t)
	}
	if t.dataStreams != nil {
		t.dataStreams.Start()
	}
	// Start AppSec with remote configuration
	cfg := remoteconfig.DefaultClientConfig()
	cfg.AgentURL = t.config.agentURL.String()
	cfg.AppVersion = t.config.version
	cfg.Env = t.config.env
	cfg.HTTP = t.config.httpClient
	cfg.ServiceName = t.config.serviceName
	appsec.Start(appsec.WithRCConfig(cfg))
	// start instrumentation telemetry unless it is disabled through the
	// DD_INSTRUMENTATION_TELEMETRY_ENABLED env var
	startTelemetry(t.config)
	_ = t.hostname() // Prime the hostname cache
}

// Stop stops the started tracer. Subsequent calls are valid but become no-op.
func Stop() {
	ddtrace.SetGlobalTracer(&ddtrace.NoopTracer{})
	log.Flush()
}

// Extract extracts a SpanContext from the carrier. The carrier is expected
// to implement TextMapReader, otherwise an error is returned.
// If the tracer is not started, calling this function is a no-op.
func Extract(carrier interface{}) (ddtrace.SpanContext, error) {
	return ddtrace.GetGlobalTracer().Extract(carrier)
}

// Inject injects the given SpanContext into the carrier. The carrier is
// expected to implement TextMapWriter, otherwise an error is returned.
// If the tracer is not started, calling this function is a no-op.
func Inject(ctx ddtrace.SpanContext, carrier interface{}) error {
	return ddtrace.GetGlobalTracer().Inject(ctx, carrier)
}

// payloadQueueSize is the buffer size of the trace channel.
const payloadQueueSize = 1000

func newUnstartedTracer(opts ...StartOption) *Tracer {
	c := newConfig(opts...)
	sampler := newPrioritySampler()
	statsd, err := newStatsdClient(c)
	if err != nil {
		log.Warn("Runtime and health metrics disabled: %v", err)
	}
	var writer traceWriter
	if c.logToStdout {
		writer = newLogTraceWriter(c, statsd)
	} else {
		writer = newAgentTraceWriter(c, sampler, statsd)
	}
	traces, spans, err := samplingRulesFromEnv()
	if err != nil {
		log.Warn("DIAGNOSTICS Error(s) parsing sampling rules: found errors:%s", err)
	}
	if traces != nil {
		c.traceRules = traces
	}
	if spans != nil {
		c.spanRules = spans
	}
	globalRate := globalSampleRate()
	rulesSampler := newRulesSampler(c.traceRules, c.spanRules, globalRate)
	c.traceSampleRate = newDynamicConfig(globalRate, rulesSampler.traces.setGlobalSampleRate)
	var dataStreamsProcessor *datastreams.Processor
	if c.dataStreamsMonitoringEnabled {
		dataStreamsProcessor = datastreams.NewProcessor(statsd, c.env, c.serviceName, c.version, c.agentURL, c.httpClient, func() bool {
			f := loadAgentFeatures(c.logToStdout, c.agentURL, c.httpClient)
			return f.DataStreams
		})
	}
	t := &Tracer{
		config:           c,
		traceWriter:      writer,
		out:              make(chan *ddtrace.Chunk, payloadQueueSize),
		stop:             make(chan struct{}),
		flush:            make(chan chan<- struct{}),
		rulesSampling:    rulesSampler,
		prioritySampling: sampler,
		pid:              os.Getpid(),
		stats:            newConcentrator(c, defaultStatsBucketSize),
		obfuscator: obfuscate.NewObfuscator(obfuscate.Config{
			SQL: obfuscate.SQLConfig{
				TableNames:       c.agent.HasFlag("table_names"),
				ReplaceDigits:    c.agent.HasFlag("quantize_sql_tables") || c.agent.HasFlag("replace_sql_digits"),
				KeepSQLAlias:     c.agent.HasFlag("keep_sql_alias"),
				DollarQuotedFunc: c.agent.HasFlag("dollar_quoted_func"),
				Cache:            c.agent.HasFlag("sql_cache"),
			},
		}),
		statsd:      statsd,
		dataStreams: dataStreamsProcessor,
	}
	return t
}

// newTracer creates a new no-op tracer for testing.
// NOTE: This function does NOT set the global tracer, which is required for
// most finish span/flushing operations to work as expected. If you are calling
// span.Finish and/or expecting flushing to work, you must call
// internal.SetGlobalTracer(...) with the tracer provided by this function.
func newTracer(opts ...StartOption) *Tracer {
	t := newUnstartedTracer(opts...)
	c := t.config
	t.statsd.Incr("datadog.tracer.started", nil, 1)
	if c.runtimeMetrics {
		log.Debug("Runtime metrics enabled.")
		t.wg.Add(1)
		go func() {
			defer t.wg.Done()
			t.reportRuntimeMetrics(defaultMetricsReportInterval)
		}()
	}
	if c.debugAbandonedSpans {
		log.Info("Abandoned spans logs enabled.")
		t.abandonedSpansDebugger = newAbandonedSpansDebugger()
		t.abandonedSpansDebugger.Start(t.config.spanTimeout)
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
// the tracer on each invocation may create too much latency. In this
// scenario, a tracer may be started and stopped by the parent process
// whereas the invocation can make use of Flush to ensure any created spans
// reach the agent.
func Flush() {
	if t, ok := ddtrace.GetGlobalTracer().(*Tracer); ok {
		t.flushSync()
		if t.dataStreams != nil {
			t.dataStreams.Flush()
		}
	}
}

// flushSync triggers a flush and waits for it to complete.
func (t *Tracer) flushSync() {
	done := make(chan struct{})
	t.flush <- done
	<-done
}

// worker receives finished traces to be added into the payload, as well
// as periodically flushes traces to the transport.
func (t *Tracer) worker(tick <-chan time.Time) {
	for {
		select {
		case trace := <-t.out:
			t.sampleChunk(trace)
			if len(trace.Spans) != 0 {
				t.traceWriter.add(trace.Spans)
			}
		case <-tick:
			t.statsd.Incr("datadog.tracer.flush_triggered", []string{"reason:scheduled"}, 1)
			t.traceWriter.flush()

		case done := <-t.flush:
			t.statsd.Incr("datadog.tracer.flush_triggered", []string{"reason:invoked"}, 1)
			t.traceWriter.flush()
			t.statsd.Flush()
			t.stats.flushAndSend(time.Now(), withCurrentBucket)
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
					t.sampleChunk(trace)
					if len(trace.Spans) != 0 {
						t.traceWriter.add(trace.Spans)
					}
				default:
					break loop
				}
			}
			return
		}
	}
}

// Chunk holds information about a trace Chunk to be flushed, including its spans.
// The Chunk may be a fully finished local trace Chunk, or only a portion of the local trace Chunk in the case of
// partial flushing.

// sampleChunk applies single-span sampling to the provided trace.
func (t *Tracer) sampleChunk(c *ddtrace.Chunk) {
	if len(c.Spans) > 0 {
		// TODO(kjn v2): Fix span context
		// 		if p, ok := c.Spans[0].context.samplingPriority(); ok && p > 0 {
		// 			// The trace is kept, no need to run single span sampling rules.
		// 			return
		// 		}
	}
	var kept []*ddtrace.Span
	if t.rulesSampling.HasSpanRules() {
		// Apply sampling rules to individual spans in the trace.
		for _, span := range c.Spans {
			if t.rulesSampling.SampleSpan(span) {
				kept = append(kept, span)
			}
		}
		if len(kept) > 0 && len(kept) < len(c.Spans) {
			// Some spans in the trace were kept, so a partial trace will be sent.
			atomic.AddUint32(&t.partialTraces, 1)
		}
	}
	if len(kept) == 0 {
		atomic.AddUint32(&t.droppedP0Traces, 1)
	}
	atomic.AddUint32(&t.droppedP0Spans, uint32(len(c.Spans)-len(kept)))
	if !c.WillSend {
		c.Spans = kept
	}
}

func (t *Tracer) PushChunk(trace *ddtrace.Chunk) {
	select {
	case <-t.stop:
		return
	default:
	}
	select {
	case t.out <- trace:
	default:
		log.Error("payload queue full, dropping %d traces", len(trace.Spans))
	}
}

// StartSpan creates, starts, and returns a new Span with the given `operationName`.
func (t *Tracer) StartSpan(operationName string, options ...ddtrace.StartSpanOption) *ddtrace.Span {
	s, _ := t.StartSpanFromContext(context.Background(), operationName, options...)
	return s
}

// withContext associates the ctx with the span.
func withContext(ctx context.Context) ddtrace.StartSpanOption {
	return func(cfg *ddtrace.StartSpanConfig) {
		cfg.Context = ctx
	}
}

// TODO(kjn v2): This probably doesn't belong in the tracer.
// We should be able to start a span without the tracer itself.
func (t *Tracer) StartSpanFromContext(ctx context.Context, operationName string, options ...ddtrace.StartSpanOption) (*ddtrace.Span, context.Context) {
	// copy opts in case the caller reuses the slice in parallel
	// we will add at least 1, at most 2 items
	optsLocal := make([]ddtrace.StartSpanOption, len(options), len(options)+2)
	copy(optsLocal, options)

	if ctx == nil {
		// default to context.Background() to avoid panics on Go >= 1.15
		ctx = context.Background()
	} else if s, ok := ddtrace.SpanFromContext(ctx); ok {
		optsLocal = append(optsLocal, ddtrace.ChildOf(s.Context()))
	}
	options = optsLocal

	var opts ddtrace.StartSpanConfig
	for _, fn := range options {
		fn(&opts)
	}
	var startTime int64
	if opts.StartTime.IsZero() {
		startTime = ddtrace.Now()
	} else {
		startTime = opts.StartTime.UnixNano()
	}
	// TODO(kjn v2): Fix span context
	//var context *spanContext
	// The default pprof context is taken from the start options and is
	// not nil when using StartSpanFromContext()
	pprofContext := opts.Context
	if opts.Parent != nil {
		// TODO(kjn v2): Fix span context
		// 		if ctx, ok := opts.Parent.(*spanContext); ok {
		// 			context = ctx
		// 			if pprofContext == nil && ctx.span != nil {
		// 				// Inherit the context.Context from parent span if it was propagated
		// 				// using ChildOf() rather than StartSpanFromContext(), see
		// 				// applyPPROFLabels() below.
		// 				pprofContext = ctx.span.pprofCtxActive
		// 			}
		// 		} else if p, ok := opts.Parent.(ddtrace.SpanContextW3C); ok {
		// 			context = &spanContext{
		// 				traceID: p.TraceID128Bytes(),
		// 				spanID:  p.SpanID(),
		// 			}
		// 		}
	}
	if pprofContext == nil {
		// For root span's without context, there is no pprofContext, but we need
		// one to avoid a panic() in pprof.WithLabels(). Using context.Background()
		// is not ideal here, as it will cause us to remove all labels from the
		// goroutine when the span finishes. However, the alternatives of not
		// applying labels for such spans or to leave the endpoint/hotspot labels
		// on the goroutine after it finishes are even less appealing. We'll have
		// to properly document this for users.
		pprofContext = gocontext.Background()
	}
	id := opts.SpanID
	if id == 0 {
		id = generateSpanID(startTime)
	}
	// span defaults
	span := &ddtrace.Span{
		Name:     operationName,
		Service:  t.config.serviceName,
		Resource: operationName,
		SpanID:   id,
		TraceID:  id,
		Start:    startTime,
		// TODO(kjn v2): Fix span start
		// noDebugStack: t.config.noDebugStack,
		// tracer: t,
	}
	if t.config.hostname != "" {
		// TODO(kjn v2): More setMeta
		//span.setMeta(keyHostname, t.config.hostname)
		span.SetTag(keyHostname, t.config.hostname)
	}
	// TODO(kjn v2): Fix span context
	// 	if context != nil {
	// 		// this is a child span
	// 		span.TraceID = context.traceID.Lower()
	// 		span.ParentID = context.spanID
	// 		if p, ok := context.samplingPriority(); ok {
	// 			span.setMetric(keySamplingPriority, float64(p))
	// 		}
	// 		if context.span != nil {
	// 			// local parent, inherit service
	// 			context.span.RLock()
	// 			span.Service = context.span.Service
	// 			context.span.RUnlock()
	// 		} else {
	// 			// remote parent
	// 			if context.origin != "" {
	// 				// mark origin
	// 				span.setMeta(keyOrigin, context.origin)
	// 			}
	// 		}
	// 	}
	// 	span.context = newSpanContext(span, context)
	// TODO(kjn v2): More setMeta / setMetric
	// span.setMetric(ext.Pid, float64(t.pid))
	// span.setMeta("language", "go")
	span.SetTag(ext.Pid, float64(t.pid))
	span.SetTag("language", "go")

	// add tags from options
	for k, v := range opts.Tags {
		span.SetTag(k, v)
	}
	// add global tags
	for k, v := range t.config.globalTags {
		span.SetTag(k, v)
	}
	if t.config.serviceMappings != nil {
		if newSvc, ok := t.config.serviceMappings[span.Service]; ok {
			span.Service = newSvc
		}
	}
	// TODO(kjn v2): Fix span context
	// 	isRootSpan := context == nil || context.span == nil
	// 	if isRootSpan {
	// 		traceprof.SetProfilerRootTags(span)
	// 		span.setMetric(keySpanAttributeSchemaVersion, float64(t.config.spanAttributeSchemaVersion))
	// 	}
	// 	if isRootSpan || context.span.Service != span.Service {
	// 		span.setMetric(keyTopLevel, 1)
	// 		// all top level spans are measured. So the measured tag is redundant.
	// 		delete(span.Metrics, keyMeasured)
	// 	}
	if t.config.version != "" {
		if t.config.universalVersion || (!t.config.universalVersion && span.Service == t.config.serviceName) {
			// TODO(kjn v2): More setMeta
			// span.setMeta(ext.Version, t.config.version)
			span.SetTag(ext.Version, t.config.version)
		}
	}
	if t.config.env != "" {
		// TODO(kjn v2): More setMeta
		// span.setMeta(ext.Environment, t.config.env)
		span.SetTag(ext.Environment, t.config.env)
	}
	// TODO(kjn v2): Fix span context
	// 	if _, ok := span.context.samplingPriority(); !ok {
	// 		// if not already sampled or a brand new trace, sample it
	// 		t.sample(span)
	// 	}

	// TODO(kjn v2): This stuff needs to be in the span maybe? Dunno why we're setting span attributes here.
	// 	pprofContext, span.taskEnd = startExecutionTracerTask(pprofContext, span)
	// 	if t.config.profilerHotspots || t.config.profilerEndpoints {
	// 		t.applyPPROFLabels(pprofContext, span)
	// 	}
	if t.config.serviceMappings != nil {
		if newSvc, ok := t.config.serviceMappings[span.Service]; ok {
			span.Service = newSvc
		}
	}
	if log.DebugEnabled() {
		// avoid allocating the ...interface{} argument if debug logging is disabled
		log.Debug("Started Span: %v, Operation: %s, Resource: %s, Tags: %v, %v",
			span, span.Name, span.Resource, span.Meta, span.Metrics)
	}
	if t.config.debugAbandonedSpans {
		select {
		case t.abandonedSpansDebugger.In <- newAbandonedSpanCandidate(span, false):
			// ok
		default:
			log.Error("Abandoned spans channel full, disregarding span.")
		}
	}
	// TODO(kjn v2): Should be somewhere else. These look like span internals.
	// 	if span.pprofCtxActive != nil {
	// 		// If pprof labels were applied for this span, use the derived ctx that
	// 		// includes them. Otherwise a child of this span wouldn't be able to
	// 		// correctly restore the labels of its parent when it finishes.
	// 		ctx = span.pprofCtxActive
	// 	}

	return span, ddtrace.ContextWithSpan(ctx, span)
}

// generateSpanID returns a random uint64 that has been XORd with the startTime.
// This is done to get around the 32-bit random seed limitation that may create collisions if there is a large number
// of go services all generating spans.
func generateSpanID(startTime int64) uint64 {
	return random.Uint64() ^ uint64(startTime)
}

// applyPPROFLabels applies pprof labels for the profiler's code hotspots and
// endpoint filtering feature to span. When span finishes, any pprof labels
// found in ctx are restored. Additionally, this func informs the profiler how
// many times each endpoint is called.
func (t *Tracer) applyPPROFLabels(ctx gocontext.Context, span *ddtrace.Span) {
	// TODO(kjn v2): Tracer shouldn't really be modifying span internals. Needs to either move into
	// span, or work *with* span.
	//
	// 	var labels []string
	// 	if t.config.profilerHotspots {
	// 		// allocate the max-length slice to avoid growing it later
	// 		labels = make([]string, 0, 6)
	// 		labels = append(labels, traceprof.SpanID, strconv.FormatUint(span.SpanID, 10))
	// 	}
	// 	// nil checks might not be needed, but better be safe than sorry
	// 	if localRootSpan := span.root(); localRootSpan != nil {
	// 		if t.config.profilerHotspots {
	// 			labels = append(labels, traceprof.LocalRootSpanID, strconv.FormatUint(localRootSpan.SpanID, 10))
	// 		}
	// 		if t.config.profilerEndpoints && spanResourcePIISafe(localRootSpan) {
	// 			labels = append(labels, traceprof.TraceEndpoint, localRootSpan.Resource)
	// 			if span == localRootSpan {
	// 				// Inform the profiler of endpoint hits. This is used for the unit of
	// 				// work feature. We can't use APM stats for this since the stats don't
	// 				// have enough cardinality (e.g. runtime-id tags are missing).
	// 				traceprof.GlobalEndpointCounter().Inc(localRootSpan.Resource)
	// 			}
	// 		}
	// 	}
	// 	if len(labels) > 0 {
	// 		span.pprofCtxRestore = ctx
	// 		span.pprofCtxActive = pprof.WithLabels(ctx, pprof.Labels(labels...))
	// 		pprof.SetGoroutineLabels(span.pprofCtxActive)
	// 	}
}

// Stop stops the tracer.
func (t *Tracer) Stop() {
	t.stopOnce.Do(func() {
		close(t.stop)
		t.statsd.Incr("datadog.tracer.stopped", nil, 1)
	})
	t.abandonedSpansDebugger.Stop()
	t.stats.Stop()
	t.wg.Wait()
	t.traceWriter.stop()
	t.statsd.Close()
	if t.dataStreams != nil {
		t.dataStreams.Stop()
	}
	appsec.Stop()
}

// Inject uses the configured or default TextMap Propagator.
func (t *Tracer) Inject(ctx ddtrace.SpanContext, carrier interface{}) error {
	return t.config.propagator.Inject(ctx, carrier)
}

// Extract uses the configured or default TextMap Propagator.
func (t *Tracer) Extract(carrier interface{}) (ddtrace.SpanContext, error) {
	return t.config.propagator.Extract(carrier)
}

func (t *Tracer) CanComputeStats() bool {
	return t.config.canComputeStats()
}

// sampleRateMetricKey is the metric key holding the applied sample rate. Has to be the same as the Agent.
const sampleRateMetricKey = "_sample_rate"

// Sample samples a span with the internal sampler.
func (t *Tracer) sample(span *ddtrace.Span) {
	// TODO(kjn v2): Fix span context
	// 	if _, ok := span.context.samplingPriority(); ok {
	// 		// sampling decision was already made
	// 		return
	// 	}
	sampler := t.config.sampler
	if !sampler.Sample(span) {
		// TODO(kjn v2): Fix span context
		// 		span.context.trace.drop()
		// 		span.context.trace.setSamplingPriority(ext.PriorityAutoReject, samplernames.RuleRate)
		return
	}
	if rs, ok := sampler.(RateSampler); ok && rs.Rate() < 1 {
		// TODO(kjn v2): setMetric
		// span.setMetric(sampleRateMetricKey, rs.Rate())
		span.SetTag(sampleRateMetricKey, rs.Rate())
	}
	if t.rulesSampling.SampleTrace(span) {
		return
	}
	t.prioritySampling.apply(span)
}

func startExecutionTracerTask(ctx gocontext.Context, span *ddtrace.Span) (gocontext.Context, func()) {
	if !rt.IsEnabled() {
		return ctx, func() {}
	}
	// TODO(kjn v2): Stop Mutating span state everywhere!
	//span.goExecTraced = true

	// Task name is the resource (operationName) of the span, e.g.
	// "POST /foo/bar" (http) or "/foo/pkg.Method" (grpc).
	taskName := span.Resource
	// If the resource could contain PII (e.g. SQL query that's not using bind
	// arguments), play it safe and just use the span type as the taskName,
	// e.g. "sql".
	if !span.SpanResourcePIISafe() {
		taskName = span.Type
	}
	end := noopTaskEnd
	if !globalinternal.IsExecutionTraced(ctx) {
		var task *rt.Task
		ctx, task = rt.NewTask(ctx, taskName)
		end = task.End
	} else {
		// We only want to skip task creation for this particular span,
		// not necessarily for child spans which can come from different
		// integrations. So update this context to be "not" execution
		// traced so that derived contexts used by child spans don't get
		// skipped.
		ctx = globalinternal.WithExecutionNotTraced(ctx)
	}
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], span.SpanID)
	// TODO: can we make string(b[:]) not allocate? e.g. with unsafe
	// shenanigans? rt.Log won't retain the message string, though perhaps
	// we can't assume that will always be the case.
	rt.Log(ctx, "datadog.uint64_span_id", string(b[:]))
	return ctx, end
}

func noopTaskEnd() {}

func (t *Tracer) hostname() string {
	if !t.config.enableHostnameDetection {
		return ""
	}
	return hostname.Get()
}

// TODO(kjn v2): Move into internal package and deduplicate.
const (
	keySamplingPriority     = "_sampling_priority_v1"
	keySamplingPriorityRate = "_dd.agent_psr"
	keyDecisionMaker        = "_dd.p.dm"
	keyServiceHash          = "_dd.dm.service_hash"
	keyOrigin               = "_dd.origin"
	// keyHostname can be used to override the agent's hostname detection when using `WithHostname`. Not to be confused with keyTracerHostname
	// which is set via auto-detection.
	keyHostname                = "_dd.hostname"
	keyRulesSamplerAppliedRate = "_dd.rule_psr"
	keyRulesSamplerLimiterRate = "_dd.limit_psr"
	keyMeasured                = "_dd.measured"
	// keyTopLevel is the key of top level metric indicating if a span is top level.
	// A top level span is a local root (parent span of the local trace) or the first span of each service.
	keyTopLevel = "_dd.top_level"
	// keyPropagationError holds any error from propagated trace tags (if any)
	keyPropagationError = "_dd.propagation_error"
	// keySpanSamplingMechanism specifies the sampling mechanism by which an individual span was sampled
	keySpanSamplingMechanism = "_dd.span_sampling.mechanism"
	// keySingleSpanSamplingRuleRate specifies the configured sampling probability for the single span sampling rule.
	keySingleSpanSamplingRuleRate = "_dd.span_sampling.rule_rate"
	// keySingleSpanSamplingMPS specifies the configured limit for the single span sampling rule
	// that the span matched. If there is no configured limit, then this tag is omitted.
	keySingleSpanSamplingMPS = "_dd.span_sampling.max_per_second"
	// keyPropagatedUserID holds the propagated user identifier, if user id propagation is enabled.
	keyPropagatedUserID = "_dd.p.usr.id"
	//keyTracerHostname holds the tracer detected hostname, only present when not connected over UDS to agent.
	keyTracerHostname = "_dd.tracer_hostname"
	// keyTraceID128 is the lowercase, hex encoded upper 64 bits of a 128-bit trace id, if present.
	keyTraceID128 = "_dd.p.tid"
	// keySpanAttributeSchemaVersion holds the selected DD_TRACE_SPAN_ATTRIBUTE_SCHEMA version.
	keySpanAttributeSchemaVersion = "_dd.trace_span_attribute_schema"
	// keyPeerServiceSource indicates the precursor tag that was used as the value of peer.service.
	keyPeerServiceSource = "_dd.peer.service.source"
	// keyPeerServiceRemappedFrom indicates the previous value for peer.service, in case remapping happened.
	keyPeerServiceRemappedFrom = "_dd.peer.service.remapped_from"
	// keyBaseService contains the globally configured tracer service name. It is only set for spans that override it.
	keyBaseService = "_dd.base_service"
)

// The following set of tags is used for user monitoring and set through calls to span.SetUser().
const (
	keyUserID        = "usr.id"
	keyUserEmail     = "usr.email"
	keyUserName      = "usr.name"
	keyUserRole      = "usr.role"
	keyUserScope     = "usr.scope"
	keyUserSessionID = "usr.session_id"
)

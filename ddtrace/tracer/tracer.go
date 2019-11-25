// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package tracer

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
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
}

type statsdClient interface {
	Incr(name string, tags []string, rate float64) error
	Count(name string, value int64, tags []string, rate float64) error
	Gauge(name string, value float64, tags []string, rate float64) error
	Timing(name string, value time.Duration, tags []string, rate float64) error
	Close() error
}

type noopStats struct{}

func (n *noopStats) Incr(name string, tags []string, rate float64) error {
	return nil
}

func (n *noopStats) Count(name string, value int64, tags []string, rate float64) error {
	return nil
}

func (n *noopStats) Gauge(name string, value float64, tags []string, rate float64) error {
	return nil
}

func (n *noopStats) Timing(name string, value time.Duration, tags []string, rate float64) error {
	return nil
}

func (n *noopStats) Close() error {
	return nil
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
	if c.statsd == nil {
		client, err := statsd.New(c.dogstatsdAddr, statsd.WithMaxMessagesPerPayload(40), statsd.WithTags(statsTags(c)))
		if err != nil {
			log.Warn("Runtime and tracer metrics disabled: %v", err)
			c.statsd = &noopStats{}
		} else {
			c.statsd = client
		}
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
	}
	t.config.statsd.Incr("datadog.tracer.started", nil, 1)
	if c.runtimeMetrics {
		log.Debug("Runtime metrics enabled.")
		go t.reportMetrics(defaultMetricsReportInterval)
	}
	go t.worker()
	return t
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
			t.config.statsd.Incr("datadog.trace.flush.count", []string{"reason:scheduled"}, 1)
			t.flushPayload()

		case confirm := <-t.flushChan:
			t.config.statsd.Incr("datadog.trace.flush.count", []string{"reason:size"}, 1)
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
			t.config.statsd.Incr("datadog.trace.flush.count", []string{"reason:shutdown"}, 1)
			t.flushPayload()
			t.config.statsd.Incr("datadog.tracer.stopped", nil, 1)
			t.config.statsd.Close()
			t.config.statsd = &noopStats{}
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
	defer func(start time.Time) {
		t.config.statsd.Timing("datadog.tracer.flush.duration", time.Since(start), nil, 1)
	}(time.Now())
	if t.payload.itemCount() == 0 {
		return
	}
	size, count := t.payload.size(), t.payload.itemCount()
	log.Debug("Sending payload: size: %d traces: %d\n", size, count)
	rc, err := t.config.transport.send(t.payload)
	if err != nil {
		t.config.statsd.Count("datadog.tracer.flush.traces_lost", int64(count), nil, 1)
		log.Error("lost %d traces: %v", count, err)
	} else {
		t.config.statsd.Count("datadog.tracer.flush.bytes", int64(size), nil, 1)
		t.config.statsd.Count("datadog.tracer.flush.traces", int64(count), nil, 1)
		t.prioritySampling.readRatesJSON(rc)
	}
	t.payload.reset()
}

// pushPayload pushes the trace onto the payload. If the payload becomes
// larger than the threshold as a result, it sends a flush request.
func (t *tracer) pushPayload(trace []*span) {
	if err := t.payload.push(trace); err != nil {
		t.config.statsd.Incr("datadog.tracer.payload.error", nil, 1)
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

type testStatsdClient struct {
	mu          sync.RWMutex
	gaugeCalls  []gaugeCall
	incrCalls   []incrCall
	countCalls  []countCall
	timingCalls []timingCall
	counts      map[string]int64
	tags        []string
	waitCh      chan struct{}
	n           int
	closed      bool
}

type gaugeCall struct {
	name  string
	value float64
	tags  []string
	rate  float64
}

type incrCall struct {
	name string
	tags []string
	rate float64
}

type countCall struct {
	name  string
	value int64
	tags  []string
	rate  float64
}

type timingCall struct {
	name  string
	value time.Duration
	tags  []string
	rate  float64
}

func withStats(s statsdClient) StartOption {
	return func(c *config) {
		c.statsd = s
	}
}

func (tg *testStatsdClient) addCount(name string, value int64) {
	if tg.counts == nil {
		tg.counts = make(map[string]int64)
	}
	tg.counts[name] += value
}

func (tg *testStatsdClient) Gauge(name string, value float64, tags []string, rate float64) error {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	c := gaugeCall{
		name:  name,
		value: value,
		tags:  make([]string, len(tags)),
		rate:  rate,
	}
	copy(c.tags, tags)
	tg.gaugeCalls = append(tg.gaugeCalls, c)
	tg.tags = tags
	if tg.n > 0 {
		tg.n--
		if tg.n == 0 {
			close(tg.waitCh)
		}
	}
	return nil
}

func (tg *testStatsdClient) Incr(name string, tags []string, rate float64) error {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	tg.addCount(name, 1)
	c := incrCall{
		name: name,
		tags: make([]string, len(tags)),
		rate: rate,
	}
	copy(c.tags, tags)
	tg.incrCalls = append(tg.incrCalls, c)
	tg.tags = tags
	if tg.n > 0 {
		tg.n--
		if tg.n == 0 {
			close(tg.waitCh)
		}
	}
	return nil
}

func (tg *testStatsdClient) Count(name string, value int64, tags []string, rate float64) error {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	tg.addCount(name, value)
	c := countCall{
		name:  name,
		value: value,
		tags:  make([]string, len(tags)),
		rate:  rate,
	}
	copy(c.tags, tags)
	tg.countCalls = append(tg.countCalls, c)
	tg.tags = tags
	if tg.n > 0 {
		tg.n--
		if tg.n == 0 {
			close(tg.waitCh)
		}
	}
	return nil
}

func (tg *testStatsdClient) Timing(name string, value time.Duration, tags []string, rate float64) error {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	c := timingCall{
		name:  name,
		value: value,
		tags:  make([]string, len(tags)),
		rate:  rate,
	}
	copy(c.tags, tags)
	tg.timingCalls = append(tg.timingCalls, c)
	tg.tags = tags
	if tg.n > 0 {
		tg.n--
		if tg.n == 0 {
			close(tg.waitCh)
		}
	}
	return nil
}

func (tg *testStatsdClient) Close() error {
	tg.closed = true
	return nil
}

func (tg *testStatsdClient) GaugeCalls() []gaugeCall {
	tg.mu.RLock()
	defer tg.mu.RUnlock()
	c := make([]gaugeCall, len(tg.gaugeCalls))
	copy(c, tg.gaugeCalls)
	return c
}

func (tg *testStatsdClient) IncrCalls() []incrCall {
	tg.mu.RLock()
	defer tg.mu.RUnlock()
	c := make([]incrCall, len(tg.incrCalls))
	copy(c, tg.incrCalls)
	return c
}

func (tg *testStatsdClient) CountCalls() []countCall {
	tg.mu.RLock()
	defer tg.mu.RUnlock()
	c := make([]countCall, len(tg.countCalls))
	copy(c, tg.countCalls)
	return c
}

func (tg *testStatsdClient) CallNames() []string {
	tg.mu.RLock()
	defer tg.mu.RUnlock()
	n := make([]string, 0)
	for _, c := range tg.gaugeCalls {
		n = append(n, c.name)
	}
	for _, c := range tg.incrCalls {
		n = append(n, c.name)
	}
	for _, c := range tg.countCalls {
		n = append(n, c.name)
	}
	for _, c := range tg.timingCalls {
		n = append(n, c.name)
	}
	return n
}

func (tg *testStatsdClient) CallsByName() map[string]int {
	tg.mu.RLock()
	defer tg.mu.RUnlock()
	counts := make(map[string]int)
	for _, c := range tg.gaugeCalls {
		counts[c.name]++
	}
	for _, c := range tg.incrCalls {
		counts[c.name]++
	}
	for _, c := range tg.countCalls {
		counts[c.name]++
	}
	for _, c := range tg.timingCalls {
		counts[c.name]++
	}
	return counts
}

func (tg *testStatsdClient) Counts() map[string]int64 {
	tg.mu.RLock()
	defer tg.mu.RUnlock()
	c := make(map[string]int64)
	for key, value := range tg.counts {
		c[key] = value
	}
	return c
}

func (tg *testStatsdClient) Tags() []string {
	tg.mu.RLock()
	defer tg.mu.RUnlock()
	t := make([]string, len(tg.tags))
	copy(t, tg.tags)
	return t
}

func (tg *testStatsdClient) Reset() {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	tg.gaugeCalls = tg.gaugeCalls[:0]
	tg.tags = tg.tags[:0]
	if tg.waitCh != nil {
		close(tg.waitCh)
		tg.waitCh = nil
	}
	tg.n = 0
}

// Wait blocks until n metrics have been reported using the testStatsdClient or until duration d passes.
// If d passes, or a wait is already active, an error is returned.
func (tg *testStatsdClient) Wait(n int, d time.Duration) error {
	tg.mu.Lock()
	if tg.waitCh != nil {
		tg.mu.Unlock()
		return errors.New("already waiting")
	}
	tg.waitCh = make(chan struct{})
	tg.n = n
	tg.mu.Unlock()

	select {
	case <-tg.waitCh:
		return nil
	case <-time.After(d):
		return fmt.Errorf("timed out after waiting %s for gauge events", d)
	}
}

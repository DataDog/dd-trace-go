// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	pb "github.com/DataDog/datadog-agent/pkg/proto/pbgo/trace"
	"github.com/DataDog/datadog-agent/pkg/trace/stats"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/locking"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/processtags"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	telemetrylog "github.com/DataDog/dd-trace-go/v2/internal/telemetry/log"

	"github.com/DataDog/datadog-go/v5/statsd"
)

// tracerObfuscationVersion is the newest trace-agent obfuscation version whose
// stats obfuscation the tracer reproduces. Version 2 agents run a non-default
// SQL obfuscation config, which the tracer reads from /info and applies.
var tracerObfuscationVersion = 2

// defaultStatsBucketSize specifies the default span of time that will be
// covered in one stats bucket.
var defaultStatsBucketSize = (10 * time.Second).Nanoseconds()

// statsConcentrator abstracts the stats-computation lifecycle so that callers
// don't need nil checks when stats are disabled (e.g. OTLP export mode).
type statsConcentrator interface {
	Start()
	Stop()
	flushAndSend(now time.Time, includeCurrent bool)
	newTracerStatSpan(s *Span, obfuscator *statsObfuscator) (*tracerStatSpan, bool)
	trySendSpan(s *tracerStatSpan)
	trySendSpans(spans []*tracerStatSpan)
}

// concentrator aggregates and stores statistics on incoming spans in time buckets,
// flushing them occasionally to the underlying transport located in the given
// tracer config.
type concentrator struct {
	// In specifies the channel to be used for feeding data to the concentrator.
	// In order for In to have a consumer, the concentrator must be started using
	// a call to Start.
	In chan []*tracerStatSpan

	// stopped reports whether the concentrator is stopped (when non-zero)
	stopped uint32 // +checkatomic

	// spanConcentrator creates stat spans. They are aggregated in policyBuckets.
	spanConcentrator *stats.SpanConcentrator
	// policyMu guards policyBuckets. The constant spanConcentratorConfig and
	// created are only needed to create them.
	policyMu locking.Mutex
	// spanConcentratorConfig configures the SpanConcentrators of policyBuckets.
	spanConcentratorConfig *stats.SpanConcentratorConfig // +checklocks:policyMu
	// created is when the concentrator was created.
	created time.Time // +checklocks:policyMu
	// policyBuckets aggregates stat spans separately for each obfuscation
	// policy they were obfuscated under, in order of first use. A payload
	// reports one obfuscation version, so spans obfuscated differently never
	// share one: when the agent's /info changes mid-trace, spans of both
	// policies can keep arriving in any order without forcing a flush.
	policyBuckets []*policyBuckets // +checklocks:policyMu
	// lastFlush is the latest time policyBuckets were flushed at, in Unix
	// nanoseconds, or 0 before the first flush.
	lastFlush int64 // +checklocks:policyMu

	aggregationKey stats.PayloadAggregationKey

	wg           sync.WaitGroup        // waits for any active goroutines
	bucketSize   int64                 // the size of a bucket in nanoseconds
	stop         chan struct{}         // closing this channel triggers shutdown
	cfg          *config               // tracer startup configuration
	statsdClient internal.StatsdClient // statsd client for sending metrics.

	// otelSemantics is captured at construction to avoid reading locked config for every span.
	// This assumes OTel semantic mode remains fixed for the concentrator's lifetime. If the mode
	// becomes dynamically configurable, the concentrator must be recreated or this value updated.
	otelSemantics bool

	// sender determines where flushed stats go (the Datadog Agent or an OTLP
	// metrics endpoint) and the destination-specific policy that comes with it.
	sender statsSender
}

type statsSerializationError struct {
	format string
	prefix string
	err    error
}

func (e *statsSerializationError) Error() string {
	if e.prefix != "" {
		return e.prefix + ": " + e.err.Error()
	}
	return e.err.Error()
}
func (e *statsSerializationError) Unwrap() error { return e.err }

func reportStatsSendError(err error) {
	if serializationErr, ok := errors.AsType[*statsSerializationError](err); ok {
		log.Error("Error sending stats payload: %s", err.Error()) //errtrack:ignore reported by ReportError below
		telemetrylog.ReportError(
			"Error serializing stats payload",
			serializationErr.err,
			telemetry.WithTags([]string{"format:" + serializationErr.format}),
		)
	} else {
		log.Error("Error sending stats payload: %s", err.Error()) //errtrack:ignore agent or network failure
	}
}

// statsSender abstracts a concentrator's stats destination: how a flushed
// payload is sent, and the obfuscation/peer-tags policy for that destination.
type statsSender interface {
	// send sends csp, whose resources were obfuscated with obfuscation version
	// obfVersion (0 when left to the destination).
	send(csp *pb.ClientStatsPayload, obfVersion int, retries int, interval time.Duration) error
	// obfuscationVersion returns the obfuscation version the tracer obfuscates
	// stats with for an agent with features af, or 0 when it must leave
	// obfuscation to the destination.
	obfuscationVersion(af *agentFeatures) int
	// peerTags returns the peer tags to use for a stat span, given the
	// agent-advertised peer tags.
	peerTags(agentPeerTags []string) []string
	// httpRouteFallback reports whether the OTel http.route tag should be used
	// as a fallback for the HTTPEndpoint stats dimension when http.endpoint is
	// unset. This OTel-attribute mapping only applies to OTLP-routed stats;
	// native /v0.6/stats payloads must keep their existing semantics for spans
	// that never set ext.HTTPEndpoint.
	httpRouteFallback() bool
}

// ddStatsSender sends stats to the Datadog Agent's /v0.6/stats path.
type ddStatsSender struct {
	cfg *config
}

func (s *ddStatsSender) obfuscationVersion(af *agentFeatures) int {
	// Obfuscate if agent reports an obfuscation version AND our version is at
	// least as new. The stats are then obfuscated with the logic of the agent's
	// version, which may be older than the newest one we support.
	if af.obfuscationVersion > 0 && af.obfuscationVersion <= tracerObfuscationVersion && !af.statsSQLObfuscationUnsupported {
		return af.obfuscationVersion
	}
	return 0
}

func (s *ddStatsSender) peerTags(agentPeerTags []string) []string {
	return agentPeerTags
}

func (s *ddStatsSender) httpRouteFallback() bool {
	return false
}

func (s *ddStatsSender) send(csp *pb.ClientStatsPayload, obfVersion int, retries int, interval time.Duration) error {
	if obfVersion == 0 {
		log.Debug("Stats Obfuscation was skipped, agent will obfuscate (tracer %d, agent %d)", tracerObfuscationVersion, s.cfg.agent.load().obfuscationVersion)
	}
	return sendWithRetry(retries, interval, func() error {
		return s.cfg.ddTransport.sendStats(csp, obfVersion)
	})
}

// otlpStatsSender routes flushed stats to the OTLP metrics endpoint.
type otlpStatsSender struct {
	exporter *otlpMetricsExporter
}

func (s *otlpStatsSender) obfuscationVersion(_ *agentFeatures) int {
	// There is no Datadog Agent downstream of an OTLP concentrator to apply
	// obfuscation, so the tracer must always obfuscate locally.
	return tracerObfuscationVersion
}

func (s *otlpStatsSender) peerTags(agentPeerTags []string) []string {
	return agentPeerTags
}

func (s *otlpStatsSender) httpRouteFallback() bool {
	return true
}

func (s *otlpStatsSender) send(csp *pb.ClientStatsPayload, _ int, retries int, interval time.Duration) error {
	rms, sketchDecodeErr := buildOTLPMetricsRequest(csp, s.exporter.cfg)
	if sketchDecodeErr != nil {
		// Report once per flushed payload, before the transport retry loop. The
		// decoder logs every malformed summary locally and drops only that summary.
		telemetrylog.ReportError("stats_to_otlp_metrics: failed to decode sketch", sketchDecodeErr)
	}
	return sendWithRetry(retries, interval, func() error {
		return s.exporter.export(rms)
	})
}

// policyBuckets holds the stats buckets of the stat spans obfuscated under one
// obfuscation policy.
type policyBuckets struct {
	policy  statsObfuscationPolicy
	sc      *stats.SpanConcentrator
	lastAdd int64 // the latest end of a span added, in Unix nanoseconds
}

// idlePolicyBuckets is how many bucket intervals past the end of their latest
// span the buckets of an obfuscation policy stay before they are dropped. By then regular
// flushes have sent everything they held; dropping them also flushes any
// remainder.
const idlePolicyBuckets = 4

type tracerStatSpan struct {
	statSpan    *stats.StatSpan
	end         int64 // span end, in Unix nanoseconds
	origin      string
	version     string                 // per-span version tag; "" means use global aggKey version
	obfuscation statsObfuscationPolicy // how the resource was obfuscated
}

// newConcentrator creates a new concentrator using the given tracer
// configuration c. It creates buckets of bucketSize nanoseconds duration.
func newConcentrator(c *config, bucketSize int64, statsdClient internal.StatsdClient) *concentrator {
	sCfg := &stats.SpanConcentratorConfig{
		ComputeStatsBySpanKind:       true,
		BucketInterval:               bucketSize,
		WholeKeyCardinalityLimit:     c.internalConfig.StatsWholeKeyCardinalityLimit(),
		ResourceCardinalityLimit:     c.internalConfig.StatsResourceCardinalityLimit(),
		HTTPEndpointCardinalityLimit: c.internalConfig.StatsHTTPEndpointCardinalityLimit(),
		PeerTagsCardinalityLimit:     c.internalConfig.StatsPeerTagsCardinalityLimit(),
		OriginCardinalityLimit:       c.internalConfig.StatsOriginCardinalityLimit(),
	}
	if len(c.internalConfig.StatsAdditionalTags()) > 0 {
		sCfg.AdditionalMetricTagsCardinalityLimit = c.internalConfig.StatsAdditionalTagsCardinalityLimit()
	}
	env := c.agent.load().defaultEnv
	if c.internalConfig.Env() != "" {
		env = c.internalConfig.Env()
	}
	if env == "" {
		// We do this to avoid a panic in the stats calculation logic when env is empty
		// This should never actually happen as the agent MUST have an env configured to start-up
		// That panic will be removed in a future release at which point we can remove this
		env = "unknown-env"
		log.Debug("No DD Env found, normally the agent should have one")
	}
	gitCommitSha := ""
	if c.internalConfig.CIVisibilityEnabled() {
		// We only have this data if we're in CI Visibility
		gitCommitSha = utils.GetCITags()[constants.GitCommitSHA]
	}
	aggKey := stats.PayloadAggregationKey{
		Hostname:     c.internalConfig.Hostname(),
		Env:          env,
		Version:      c.internalConfig.Version(),
		ContainerID:  "", // This intentionally left empty as the Agent will attach the container ID only in certain situations.
		GitCommitSha: gitCommitSha,
		ImageTag:     "",
	}
	spanConcentrator := stats.NewSpanConcentrator(sCfg, time.Now())
	return &concentrator{
		In:                     make(chan []*tracerStatSpan, 10000),
		bucketSize:             bucketSize,
		stopped:                1,
		cfg:                    c,
		aggregationKey:         aggKey,
		spanConcentrator:       spanConcentrator,
		spanConcentratorConfig: sCfg,
		created:                time.Now(),
		statsdClient:           statsdClient,
		otelSemantics:          c.internalConfig.OTelSemanticsEnabled(),
		sender:                 &ddStatsSender{cfg: c},
	}
}

// alignTs returns the provided timestamp truncated to the bucket size.
// It gives us the start time of the time bucket in which such timestamp falls.
func alignTs(ts, bucketSize int64) int64 { return ts - ts%bucketSize }

// Start starts the concentrator. A started concentrator needs to be stopped
// in order to gracefully shut down, using Stop.
func (c *concentrator) Start() {
	if atomic.SwapUint32(&c.stopped, 0) == 0 {
		// already running
		log.Warn("(*concentrator).Start called more than once. This is likely a programming error.")
		return
	}
	c.stop = make(chan struct{})
	c.wg.Go(func() {
		tick := time.NewTicker(time.Duration(c.bucketSize) * time.Nanosecond)
		defer tick.Stop()
		c.runFlusher(tick.C)
	})
	c.wg.Go(func() {
		c.runIngester()
	})
}

// runFlusher runs the flushing loop which sends stats to the underlying transport.
func (c *concentrator) runFlusher(tick <-chan time.Time) {
	for {
		select {
		case now := <-tick:
			c.flushAndSend(now, withoutCurrentBucket)
		case <-c.stop:
			return
		}
	}
}

// statsd returns any tracer configured statsd client, or a no-op.
func (c *concentrator) statsd() internal.StatsdClient {
	if c.statsdClient == nil {
		return &statsd.NoOpClientDirect{}
	}
	return c.statsdClient
}

// runIngester runs the loop which accepts incoming data on the concentrator's In
// channel.
func (c *concentrator) runIngester() {
	for {
		select {
		case spans := <-c.In:
			_ = c.statsd().Count("datadog.tracer.stats.spans_in", int64(len(spans)), nil, 1)
			for _, s := range spans {
				c.add(s)
			}
		case <-c.stop:
			return
		}
	}
}

// +checklocksignore — Post-finish: reads finished span fields during stats computation.
func (c *concentrator) newTracerStatSpan(s *Span, obfuscator *statsObfuscator) (*tracerStatSpan, bool) {
	agentInfo := c.cfg.agent.load()
	resource := s.resource
	policy := statsObfuscationPolicy{version: c.sender.obfuscationVersion(&agentInfo)}
	if policy.version > 0 {
		policy.sql = agentInfo.statsSQLObfuscation
		resource = obfuscatedResource(obfuscator.forConfig(policy.sql), s.spanType, s.resource)
		c.spanConcentrator.SetObfuscationEnabled(true, agentInfo.HasFlag("big_resource"))
	} else {
		c.spanConcentrator.SetObfuscationEnabled(false, false)
	}
	httpMethod := ""
	if c.otelSemantics {
		httpMethod, _ = s.meta.Get(ext.HTTPRequestMethod)
	}
	if httpMethod == "" {
		httpMethod, _ = s.meta.Get(ext.HTTPMethod)
	}
	httpEndpoint, _ := s.meta.Get(ext.HTTPEndpoint)
	if httpEndpoint == "" && c.sender.httpRouteFallback() {
		// http.endpoint (net/http, mux, httptreemux, httprouter) and http.route
		// (chi, fiber, gin, echo, go-restful) are set by disjoint sets of
		// contribs, so this never overrides an explicit http.endpoint value.
		// Only OTLP-routed stats map http.route this way; native /v0.6/stats
		// payloads keep their existing empty-httpEndpoint behavior.
		httpEndpoint, _ = s.meta.Get(ext.HTTPRoute)
	}

	peerTags := c.sender.peerTags(agentInfo.peerTags)
	spanMeta := s.meta.Map(false) // stats reads span.kind, _dd.svc_src, status codes, peer tags — no promoted keys needed
	statSpan, ok := c.spanConcentrator.NewStatSpanWithConfig(stats.StatSpanConfig{
		Service:                 s.service,
		Resource:                resource,
		Name:                    s.name,
		Type:                    s.spanType,
		ParentID:                s.parentID,
		Start:                   s.start,
		Duration:                s.duration,
		Error:                   s.error,
		Meta:                    spanMeta,
		Metrics:                 s.metrics,
		PeerTags:                peerTags,
		AdditionalMetricTagKeys: c.cfg.internalConfig.StatsAdditionalTags(),
		HTTPMethod:              httpMethod,
		HTTPEndpoint:            httpEndpoint,
	})
	if !ok {
		return nil, false
	}
	origin, _ := s.meta.Get(keyOrigin)
	version, _ := s.meta.Version()
	return &tracerStatSpan{
		statSpan:    statSpan,
		end:         s.start + s.duration,
		origin:      origin,
		version:     version,
		obfuscation: policy,
	}, true
}

// add s into the concentrator's internal stats buckets.
func (c *concentrator) add(s *tracerStatSpan) {
	aggKey := c.aggregationKey
	if s.version != "" {
		aggKey.Version = s.version
	}
	c.policyMu.Lock()
	b := c.bucketsFor(s.obfuscation)
	b.lastAdd = max(b.lastAdd, s.end)
	b.sc.AddSpan(s.statSpan, aggKey, "", nil, s.origin)
	c.policyMu.Unlock()
}

// bucketsFor returns the buckets of policy, creating them if needed.
// +checklocks:c.policyMu
func (c *concentrator) bucketsFor(policy statsObfuscationPolicy) *policyBuckets {
	// The newest policy is the likeliest match.
	for _, b := range slices.Backward(c.policyBuckets) {
		if b.policy == policy {
			return b
		}
	}
	sc := stats.NewSpanConcentrator(c.spanConcentratorConfig, c.created)
	if c.lastFlush > 0 {
		// A flush moves forward the oldest bucket that accepts spans, and
		// spans ending before it are counted in it. Flushing the empty
		// concentrator at the latest flush time gives it the oldest bucket of
		// a concentrator that was flushed at every flush so far, so late spans
		// land in the same bucket whichever policy buckets exist.
		sc.Flush(c.lastFlush, false)
	}
	b := &policyBuckets{policy: policy, sc: sc}
	c.policyBuckets = append(c.policyBuckets, b)
	return b
}

// flushedStats is a set of flushed stats payloads whose resources were
// obfuscated with obfuscation version obfVersion.
type flushedStats struct {
	csps       []*pb.ClientStatsPayload
	obfVersion int
}

// flushPolicyBuckets flushes the buckets of every obfuscation policy, and
// drops the buckets of policies that have been idle long enough; they are
// recreated, from the same oldest bucket, when spans obfuscated under their
// policy arrive again.
func (c *concentrator) flushPolicyBuckets(now int64, includeCurrent bool) (flushed []flushedStats, bc stats.BlockCounts) {
	c.policyMu.Lock()
	defer c.policyMu.Unlock()
	c.lastFlush = max(c.lastFlush, now)
	kept := c.policyBuckets[:0]
	for _, b := range c.policyBuckets {
		drop := now-b.lastAdd > idlePolicyBuckets*c.bucketSize
		if csps := b.sc.Flush(now, includeCurrent || drop); len(csps) > 0 {
			flushed = append(flushed, flushedStats{csps: csps, obfVersion: b.policy.version})
		}
		addBlockCounts(&bc, b.sc.DrainBlockCounts())
		if !drop {
			kept = append(kept, b)
		}
	}
	clear(c.policyBuckets[len(kept):])
	c.policyBuckets = kept
	return flushed, bc
}

func addBlockCounts(dst *stats.BlockCounts, src stats.BlockCounts) {
	dst.LengthBlocks += src.LengthBlocks
	dst.CapBlocks += src.CapBlocks
	dst.WholeKeyCollapses += src.WholeKeyCollapses
	dst.ResourceCollapses += src.ResourceCollapses
	dst.HTTPEndpointCollapses += src.HTTPEndpointCollapses
	dst.PeerTagsCollapses += src.PeerTagsCollapses
	dst.OriginCollapses += src.OriginCollapses
}

// Stop stops the concentrator and blocks until the operation completes.
func (c *concentrator) Stop() {
	if atomic.SwapUint32(&c.stopped, 1) > 0 {
		return
	}
	close(c.stop)
	c.wg.Wait()
	c.drainIn()
	c.flushAndSend(time.Now(), withCurrentBucket)
}

// drainIn synchronously processes any spans already queued on c.In without
// blocking for further additions.
func (c *concentrator) drainIn() {
	for {
		select {
		case spans := <-c.In:
			_ = c.statsd().Count("datadog.tracer.stats.spans_in", int64(len(spans)), nil, 1)
			for _, s := range spans {
				c.add(s)
			}
		default:
			return
		}
	}
}

const (
	withCurrentBucket    = true
	withoutCurrentBucket = false
)

func sendWithRetry(retries int, interval time.Duration, fn func() error) error {
	var err error
	for attempt := 0; attempt <= retries; attempt++ {
		if err = fn(); err == nil {
			return nil
		}
		if attempt < retries {
			time.Sleep(interval)
		}
	}
	return err
}

// flushAndSend flushes all stats buckets and sends them; the current bucket is included only when includeCurrent is true.
// Stats go to the OTLP metrics endpoint when an OTLP exporter is configured; otherwise to the agent's /v0.6/stats path.
func (c *concentrator) flushAndSend(timenow time.Time, includeCurrent bool) {
	// When flushing the current bucket (e.g. tracer.Flush()), drain any spans
	// that have been sent to c.In but not yet processed by runIngester so they
	// are included in the flush rather than silently dropped.
	if includeCurrent {
		c.drainIn()
	}
	flushed, bc := c.flushPolicyBuckets(timenow.UnixNano(), includeCurrent)
	// Length blocks are counted when stat spans are created.
	addBlockCounts(&bc, c.spanConcentrator.DrainBlockCounts())
	c.emitCollapseMetrics(bc)
	for _, f := range flushed {
		c.sendPayloads(f.csps, f.obfVersion)
	}
}

// sendPayloads sends flushed stats payloads, whose resources were obfuscated
// with obfuscation version obfVersion.
func (c *concentrator) sendPayloads(csps []*pb.ClientStatsPayload, obfVersion int) {
	if len(csps) == 0 {
		// nothing to flush
		return
	}
	c.statsd().Incr("datadog.tracer.stats.flush_payloads", nil, float64(len(csps)))
	flushedBuckets := 0
	// Given we use a constant PayloadAggregationKey there should only ever be 1 of these, but to be forward
	// compatible in case this ever changes we can just iterate through all of them.
	sendRetries := c.cfg.internalConfig.SendRetries()
	retryInterval := c.cfg.internalConfig.RetryInterval()
	for _, csp := range csps {
		csp.RuntimeID = globalconfig.RuntimeID()
		csp.Service = c.cfg.internalConfig.ServiceName()
		csp.ProcessTags = processtags.GlobalTags().String()
		flushedBuckets += len(csp.Stats)
		err := c.sender.send(csp, obfVersion, sendRetries, retryInterval)
		if err != nil {
			c.statsd().Incr("datadog.tracer.stats.flush_errors", nil, 1)
			reportStatsSendError(err)
		}
	}
	c.statsd().Incr("datadog.tracer.stats.flush_buckets", nil, float64(flushedBuckets))
}

// newOTLPMetricsConcentrator creates a concentrator that exports to the OTLP metrics endpoint.
func newOTLPMetricsConcentrator(c *config, statsdClient internal.StatsdClient) *concentrator {
	conc := newConcentrator(c, c.internalConfig.OTLPMetricsFlushInterval().Nanoseconds(), statsdClient)
	conc.sender = &otlpStatsSender{exporter: newOTLPMetricsExporter(c.internalConfig)}
	return conc
}

// emitCollapseMetrics sends health and instrumentation telemetry for cardinality collapse events.
// Per the Cardinality Limits RFC:
//   - Health metric:  datadog.tracer.stats.collapsed_spans  (statsd, public)
//   - Telemetry metric: tracers.stats_collapsed_spans       (instrumentation telemetry, internal)
//
// Tags follow the RFC: collapsed:<field>, collapsed:whole_key, oversized:additional_metric_tags.
func (c *concentrator) emitCollapseMetrics(bc stats.BlockCounts) {
	type collapseEntry struct {
		count int64
		tag   string
	}
	entries := []collapseEntry{
		{bc.LengthBlocks, "oversized:additional_metric_tags"},
		{bc.CapBlocks, "collapsed:additional_metric_tags"},
		{bc.ResourceCollapses, "collapsed:resource"},
		{bc.HTTPEndpointCollapses, "collapsed:http_endpoint"},
		{bc.PeerTagsCollapses, "collapsed:peer_tags"},
		{bc.OriginCollapses, "collapsed:origin"},
		{bc.WholeKeyCollapses, "collapsed:whole_key"},
	}
	anyCollapse := false
	for _, e := range entries {
		if e.count <= 0 {
			continue
		}
		anyCollapse = true
		tags := []string{e.tag}
		// Health metric (statsd, may be off by default in some deployments)
		c.statsd().Count("datadog.tracer.stats.collapsed_spans", e.count, tags, 1)
		// Instrumentation telemetry (on by default, internal-facing)
		telemetry.Count(telemetry.NamespaceTracers, "stats_collapsed_spans", tags).Submit(float64(e.count))
	}
	if anyCollapse {
		log.Debug("Client-side stats values are being collapsed to 'tracer_blocked_value' in the current flush window. " +
			"This is caused by a tag value exceeding 200 characters, or by exceeding one of the DD_TRACE_STATS_*_CARDINALITY_LIMIT caps.")
	}
}

// trySendSpan attempts a non-blocking send of the stat span to the
// concentrator's input channel.
func (c *concentrator) trySendSpan(s *tracerStatSpan) {
	c.trySendSpans([]*tracerStatSpan{s})
}

// trySendSpans attempts a non-blocking send of stat spans to the
// concentrator's input channel.
func (c *concentrator) trySendSpans(spans []*tracerStatSpan) {
	select {
	case c.In <- spans:
	default:
		log.Error("Stats channel full, disregarding span batch.") //errtrack:ignore per-span queue capacity limit
	}
}

// noopConcentrator is a no-op implementation of statsConcentrator used when
// client-side stats are disabled (e.g. OTLP export mode).
type noopConcentrator struct{}

func (c *noopConcentrator) Start()                           {}
func (c *noopConcentrator) Stop()                            {}
func (c *noopConcentrator) flushAndSend(_ time.Time, _ bool) {}
func (c *noopConcentrator) newTracerStatSpan(_ *Span, _ *statsObfuscator) (*tracerStatSpan, bool) {
	return nil, false
}
func (c *noopConcentrator) trySendSpan(_ *tracerStatSpan)    {}
func (c *noopConcentrator) trySendSpans(_ []*tracerStatSpan) {}

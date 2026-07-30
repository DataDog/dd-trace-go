// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/DataDog/datadog-agent/pkg/obfuscate"
	pb "github.com/DataDog/datadog-agent/pkg/proto/pbgo/trace"
	"github.com/DataDog/datadog-agent/pkg/trace/stats"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/processtags"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"

	"github.com/DataDog/datadog-go/v5/statsd"
)

// tracerObfuscationVersion indicates which version of stats obfuscation logic we implement
// In the future this can be pulled directly from our obfuscation import.
var tracerObfuscationVersion = 1

// defaultStatsBucketSize specifies the default span of time that will be
// covered in one stats bucket.
var defaultStatsBucketSize = (10 * time.Second).Nanoseconds()

// statsConcentrator abstracts the stats-computation lifecycle so that callers
// don't need nil checks when stats are disabled (e.g. OTLP export mode).
type statsConcentrator interface {
	Start()
	Stop()
	flushAndSend(now time.Time, includeCurrent bool)
	newTracerStatSpan(s *Span, obfuscator *obfuscate.Obfuscator) (*tracerStatSpan, bool)
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

	spanConcentrator *stats.SpanConcentrator

	aggregationKey stats.PayloadAggregationKey

	wg           sync.WaitGroup        // waits for any active goroutines
	bucketSize   int64                 // the size of a bucket in nanoseconds
	stop         chan struct{}         // closing this channel triggers shutdown
	cfg          *config               // tracer startup configuration
	statsdClient internal.StatsdClient // statsd client for sending metrics.

	// sender determines where flushed stats go (the Datadog Agent or an OTLP
	// metrics endpoint) and the destination-specific policy that comes with it.
	sender statsSender
}

// statsSender abstracts a concentrator's stats destination: how a flushed
// payload is sent, and the obfuscation/peer-tags policy for that destination.
type statsSender interface {
	send(csp *pb.ClientStatsPayload, retries int, interval time.Duration) error
	shouldObfuscate() bool
	// peerTags returns the peer tags to use for a stat span, given the
	// agent-advertised peer tags.
	peerTags(agentPeerTags []string) []string
}

// ddStatsSender sends stats to the Datadog Agent's /v0.6/stats path.
type ddStatsSender struct {
	cfg *config
}

func (s *ddStatsSender) shouldObfuscate() bool {
	// Obfuscate if agent reports an obfuscation version AND our version is at least as new.
	agentObfVersion := s.cfg.agent.load().obfuscationVersion
	return agentObfVersion > 0 && agentObfVersion <= tracerObfuscationVersion
}

func (s *ddStatsSender) peerTags(agentPeerTags []string) []string {
	return agentPeerTags
}

func (s *ddStatsSender) send(csp *pb.ClientStatsPayload, retries int, interval time.Duration) error {
	obfVersion := 0
	if s.shouldObfuscate() {
		obfVersion = tracerObfuscationVersion
	} else {
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

func (s *otlpStatsSender) shouldObfuscate() bool {
	// There is no Datadog Agent downstream of an OTLP concentrator to apply
	// obfuscation, so the tracer must always obfuscate locally.
	return true
}

func (s *otlpStatsSender) peerTags(_ []string) []string {
	// Custom OTLP peer tags aren't implemented yet (spec: "Out of scope for
	// SDK implementation"); suppress agent-advertised peer tags instead of
	// leaving stale DD-agent state on an OTLP-routed concentrator.
	return []string{}
}

func (s *otlpStatsSender) send(csp *pb.ClientStatsPayload, retries int, interval time.Duration) error {
	return sendWithRetry(retries, interval, func() error {
		return s.exporter.export(csp)
	})
}

type tracerStatSpan struct {
	statSpan *stats.StatSpan
	origin   string
	version  string // per-span version tag; "" means use global aggKey version
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
		In:               make(chan []*tracerStatSpan, 10000),
		bucketSize:       bucketSize,
		stopped:          1,
		cfg:              c,
		aggregationKey:   aggKey,
		spanConcentrator: spanConcentrator,
		statsdClient:     statsdClient,
		sender:           &ddStatsSender{cfg: c},
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
func (c *concentrator) newTracerStatSpan(s *Span, obfuscator *obfuscate.Obfuscator) (*tracerStatSpan, bool) {
	agentInfo := c.cfg.agent.load()
	resource := s.resource
	if c.sender.shouldObfuscate() {
		resource = obfuscatedResource(obfuscator, s.spanType, s.resource)
		c.spanConcentrator.SetObfuscationEnabled(true, agentInfo.HasFlag("big_resource"))
	} else {
		c.spanConcentrator.SetObfuscationEnabled(false, false)
	}
	httpMethod, _ := s.meta.Get(ext.HTTPMethod)
	httpEndpoint, _ := s.meta.Get(ext.HTTPEndpoint)
	if httpEndpoint == "" {
		// http.endpoint (net/http, mux, httptreemux, httprouter) and http.route
		// (chi, fiber, gin, echo, go-restful) are set by disjoint sets of
		// contribs, so this never overrides an explicit http.endpoint value.
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
		statSpan: statSpan,
		origin:   origin,
		version:  version,
	}, true
}

// add s into the concentrator's internal stats buckets.
func (c *concentrator) add(s *tracerStatSpan) {
	aggKey := c.aggregationKey
	if s.version != "" {
		aggKey.Version = s.version
	}
	c.spanConcentrator.AddSpan(s.statSpan, aggKey, "", nil, s.origin)
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
	csps := c.spanConcentrator.Flush(timenow.UnixNano(), includeCurrent)
	bc := c.spanConcentrator.DrainBlockCounts()
	c.emitCollapseMetrics(bc)
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
		err := c.sender.send(csp, sendRetries, retryInterval)
		if err != nil {
			c.statsd().Incr("datadog.tracer.stats.flush_errors", nil, 1)
			log.Error("Error sending stats payload: %s", err.Error())
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
		log.Error("Stats channel full, disregarding span batch.")
	}
}

// noopConcentrator is a no-op implementation of statsConcentrator used when
// client-side stats are disabled (e.g. OTLP export mode).
type noopConcentrator struct{}

func (c *noopConcentrator) Start()                           {}
func (c *noopConcentrator) Stop()                            {}
func (c *noopConcentrator) flushAndSend(_ time.Time, _ bool) {}
func (c *noopConcentrator) newTracerStatSpan(_ *Span, _ *obfuscate.Obfuscator) (*tracerStatSpan, bool) {
	return nil, false
}
func (c *noopConcentrator) trySendSpan(_ *tracerStatSpan)    {}
func (c *noopConcentrator) trySendSpans(_ []*tracerStatSpan) {}

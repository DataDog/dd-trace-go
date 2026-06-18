// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"maps"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DataDog/datadog-agent/pkg/obfuscate"
	"github.com/DataDog/datadog-agent/pkg/trace/stats"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/processtags"

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
}

// concentrator aggregates and stores statistics on incoming spans in time buckets,
// flushing them occasionally to the underlying transport located in the given
// tracer config.
type concentrator struct {
	// In specifies the channel to be used for feeding data to the concentrator.
	// In order for In to have a consumer, the concentrator must be started using
	// a call to Start.
	In chan *tracerStatSpan

	// stopped reports whether the concentrator is stopped (when non-zero)
	stopped uint32 // +checkatomic

	spanConcentrator *stats.SpanConcentrator

	aggregationKey stats.PayloadAggregationKey

	wg           sync.WaitGroup        // waits for any active goroutines
	bucketSize   int64                 // the size of a bucket in nanoseconds
	stop         chan struct{}         // closing this channel triggers shutdown
	cfg          *config               // tracer startup configuration
	statsdClient internal.StatsdClient // statsd client for sending metrics.

	// otlpExporter, when non-nil, routes flushed stats to the OTLP metrics
	// endpoint instead of the agent's native /v0.6/stats path.
	otlpExporter *otlpMetricsExporter

	// otlpPeerTags, when non-nil, replaces agent-advertised peer tags with a
	// fixed set of OTel semantic-convention dimensions for OTLP span metrics.
	otlpPeerTags []string
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
		ComputeStatsBySpanKind: true,
		BucketInterval:         defaultStatsBucketSize,
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
		In:               make(chan *tracerStatSpan, 10000),
		bucketSize:       bucketSize,
		stopped:          1,
		cfg:              c,
		aggregationKey:   aggKey,
		spanConcentrator: spanConcentrator,
		statsdClient:     statsdClient,
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
		case s := <-c.In:
			c.statsd().Incr("datadog.tracer.stats.spans_in", nil, 1)
			c.add(s)
		case <-c.stop:
			return
		}
	}
}

// +checklocksignore — Post-finish: reads finished span fields during stats computation.
func (c *concentrator) newTracerStatSpan(s *Span, obfuscator *obfuscate.Obfuscator) (*tracerStatSpan, bool) {
	resource := s.resource
	if c.shouldObfuscate() {
		resource = obfuscatedResource(obfuscator, s.spanType, s.resource)
	}
	httpMethod, _ := s.meta.Get(ext.HTTPMethod)
	httpEndpoint, _ := s.meta.Get(ext.HTTPEndpoint)

	peerTags := c.cfg.agent.load().peerTags
	spanMeta := s.meta.Map(false) // stats reads span.kind, _dd.svc_src, status codes, peer tags — no promoted keys needed
	if c.otlpPeerTags != nil {
		peerTags = c.otlpPeerTags
		// The stats library's matchingPeerTags only extracts peer tags for spans with
		// span.kind "client", "producer", or "consumer". DD spans (e.g. typestr="web") do
		// not carry an OTel span kind, so the library returns nil and peer tags are lost.
		// We inject span.kind="client" only when the span actually carries a peer-tag
		// value, so non-top-level unmeasured spans without peer tags are not made
		// eligible for stats via eligibleSpanKind.
		if _, hasKind := spanMeta[ext.SpanKind]; !hasKind {
			hasPeerTag := false
			for _, k := range c.otlpPeerTags {
				if _, ok := spanMeta[k]; ok {
					hasPeerTag = true
					break
				}
			}
			if hasPeerTag {
				spanMeta = make(map[string]string, len(spanMeta)+1)
				maps.Copy(spanMeta, s.meta.Map(false))
				spanMeta[ext.SpanKind] = ext.SpanKindClient
			}
		}
	}
	statSpan, ok := c.spanConcentrator.NewStatSpanWithConfig(stats.StatSpanConfig{
		Service:      s.service,
		Resource:     resource,
		Name:         s.name,
		Type:         s.spanType,
		ParentID:     s.parentID,
		Start:        s.start,
		Duration:     s.duration,
		Error:        s.error,
		Meta:         spanMeta,
		Metrics:      s.metrics,
		PeerTags:     peerTags,
		HTTPMethod:   httpMethod,
		HTTPEndpoint: httpEndpoint,
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

func (c *concentrator) shouldObfuscate() bool {
	// Obfuscate if agent reports an obfuscation version AND our version is at least as new
	agentObfVersion := c.cfg.agent.load().obfuscationVersion
	return agentObfVersion > 0 && agentObfVersion <= tracerObfuscationVersion
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
drain:
	for {
		select {
		case s := <-c.In:
			c.statsd().Incr("datadog.tracer.stats.spans_in", nil, 1)
			c.add(s)
		default:
			break drain
		}
	}
	c.flushAndSend(time.Now(), withCurrentBucket)
}

const (
	withCurrentBucket    = true
	withoutCurrentBucket = false
)

// flushAndSend flushes all the stats buckets with the given timestamp and sends them using the transport specified in
// the concentrator config. The current bucket is only included if includeCurrent is true, such as during shutdown.
// When an OTLP exporter is configured, stats are sent to the OTLP metrics endpoint; otherwise they are sent to the
// agent's native /v0.6/stats path.
func (c *concentrator) flushAndSend(timenow time.Time, includeCurrent bool) {
	// When flushing the current bucket (e.g. tracer.Flush()), drain any spans
	// that have been sent to c.In but not yet processed by runIngester so they
	// are included in the flush rather than silently dropped.
	if includeCurrent {
	drain:
		for {
			select {
			case s := <-c.In:
				c.statsd().Incr("datadog.tracer.stats.spans_in", nil, 1)
				c.add(s)
			default:
				break drain
			}
		}
	}
	csps := c.spanConcentrator.Flush(timenow.UnixNano(), includeCurrent)
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
		var err error
		if c.otlpExporter != nil {
			for attempt := 0; attempt <= sendRetries; attempt++ {
				err = c.otlpExporter.export(csp)
				if err == nil {
					break
				}
				if attempt < sendRetries {
					time.Sleep(retryInterval)
				}
			}
		} else {
			obfVersion := 0
			if c.shouldObfuscate() {
				obfVersion = tracerObfuscationVersion
			} else {
				log.Debug("Stats Obfuscation was skipped, agent will obfuscate (tracer %d, agent %d)", tracerObfuscationVersion, c.cfg.agent.load().obfuscationVersion)
			}
			for attempt := 0; attempt <= sendRetries; attempt++ {
				err = c.cfg.ddTransport.sendStats(csp, obfVersion)
				if err == nil {
					break
				}
				if attempt < sendRetries {
					time.Sleep(retryInterval)
				}
			}
		}
		if err != nil {
			c.statsd().Incr("datadog.tracer.stats.flush_errors", nil, 1)
			log.Error("Error sending stats payload: %s", err.Error())
		}
	}
	c.statsd().Incr("datadog.tracer.stats.flush_buckets", nil, float64(flushedBuckets))
}

// otlpDefaultPeerTags are span meta keys always collected as peer dimensions for
// OTLP span metrics regardless of what the Datadog agent advertises. These cover
// OTel semantic-convention attributes that have no dedicated field in
// ClientGroupedStats (unlike HTTPMethod/HTTPStatusCode which are first-class fields).
var otlpDefaultPeerTags = []string{
	"http.route",
	"grpc.method.name",
}

// newOTLPMetricsConcentrator creates a concentrator that exports flushed stats to
// the OTLP metrics endpoint instead of the agent's native /v0.6/stats path.
// The flush interval is taken from OTLPMetricsFlushInterval in cfg.
func newOTLPMetricsConcentrator(c *config, statsdClient internal.StatsdClient) *concentrator {
	bucketSize := c.internalConfig.OTLPMetricsFlushInterval().Nanoseconds()
	conc := newConcentrator(c, bucketSize, statsdClient)
	conc.otlpExporter = newOTLPMetricsExporter(c.internalConfig)
	conc.otlpPeerTags = otlpDefaultPeerTags
	return conc
}

// trySendSpan attempts a non-blocking send of the stat span to the
// concentrator's input channel.
func (c *concentrator) trySendSpan(s *tracerStatSpan) {
	select {
	case c.In <- s:
	default:
		log.Error("Stats channel full, disregarding span.")
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
func (c *noopConcentrator) trySendSpan(_ *tracerStatSpan) {}

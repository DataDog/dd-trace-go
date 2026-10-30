// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connectshared

import (
	"context"
	"maps"
	"sync"
	"sync/atomic"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

// spanBases holds one side's (client or server) read-only span configuration.
type spanBases struct {
	callOperation string
	spanKind      string
	call          *tracer.StartSpanConfig
	message       *tracer.StartSpanConfig
	callOpt       tracer.StartSpanOption
	messageOpt    tracer.StartSpanOption
}

func newSpanBases(component instrumentation.Component, pkg instrumentation.Package, analyticsRate float64) spanBases {
	b := spanBases{callOperation: operationServer, spanKind: ext.SpanKindServer}
	if component == instrumentation.ComponentClient {
		b.callOperation, b.spanKind = operationClient, ext.SpanKindClient
	}
	spanType := tracer.SpanType(ext.AppTypeRPC)
	componentTag := tracer.Tag(ext.Component, string(pkg))
	analytics := tracer.AnalyticsRate(analyticsRate)
	b.call = tracer.NewStartSpanConfig(spanType, componentTag, analytics, tracer.Tag(ext.SpanKind, b.spanKind), tracer.Measured())
	if component == instrumentation.ComponentServer {
		// Like contrib/google.golang.org/grpc, server message latency counts toward trace stats.
		b.message = tracer.NewStartSpanConfig(spanType, componentTag, analytics, tracer.Measured())
	} else {
		b.message = tracer.NewStartSpanConfig(spanType, componentTag, analytics)
	}
	b.callOpt = tracer.WithStartSpanConfig(b.call)
	b.messageOpt = tracer.WithStartSpanConfig(b.message)
	return b
}

func (c *Config) bases(component instrumentation.Component) *spanBases {
	if component == instrumentation.ComponentClient {
		return &c.client
	}
	return &c.server
}

// appendSpanOptions appends span start options in a load-bearing order. The dynamic tags must
// precede the cached base, which would otherwise alias and later mutate the base's tag map.
// Extracted parents precede the user's span options so a user parent wins, and custom tags go
// last.
func (c *Config) appendSpanOptions(opts []tracer.StartSpanOption, dynamic tracer.StartSpanOption, parentOpts []tracer.StartSpanOption, base, service tracer.StartSpanOption) []tracer.StartSpanOption {
	opts = append(opts, dynamic)
	opts = append(opts, parentOpts...)
	opts = append(opts, base, service)
	if len(c.spanOpts) == 0 && c.customTags == nil {
		return opts
	}
	opts = append(opts, c.spanOpts...)
	if c.customTags != nil {
		opts = append(opts, c.customTags)
	}
	return append(opts, preferUserService)
}

// preferUserService drops the interceptor's service when a user option sets ext.ServiceName: both
// set the span's service, and the tracer applies start tags in random map order.
func preferUserService(cfg *tracer.StartSpanConfig) {
	if _, ok := cfg.Tags[ext.ServiceName]; ok {
		delete(cfg.Tags, ext.KeyServiceSource)
	}
}

// startTagsHint sizes a span's tag map for its dynamic, base and service tags.
const startTagsHint = 12

// StartCallSpan starts the call span of an RPC of spec on side, and marks the returned context as
// handling the call that id identifies (see ClaimCall). parentOpts, such as an extracted parent,
// precede the user's span options. The span is tagged with protocol, unless protocol is nil
// because it is not known yet. Call spans always get the service option, even an empty one, so
// they don't inherit the service of a parent such as an HTTP server span.
func (c *Config) StartCallSpan(ctx context.Context, side instrumentation.Component, spec Spec, protocol *Protocol, id any, parentOpts ...tracer.StartSpanOption) (*tracer.Span, context.Context) {
	bases := c.bases(side)
	var buf [8]tracer.StartSpanOption
	var opts []tracer.StartSpanOption
	if start, ok := c.startTags(bases.call, spec, protocol); ok {
		opts = append(append(buf[:0], start), parentOpts...)
	} else {
		opts = c.appendSpanOptions(buf[:0], callTags(dynamicTags(spec, protocol)), parentOpts, bases.callOpt, c.resolveService().opt)
	}
	span, ctx := tracer.StartSpanFromContext(ctx, bases.callOperation, opts...)
	return span, c.markHandled(ctx, side, callMarker{procedure: spec.Procedure, id: id, span: span, traced: true})
}

// dynamicTags returns the tags of spec, and of protocol unless it is nil, in a map sized for every
// start tag of a span.
func dynamicTags(spec Spec, protocol *Protocol) map[string]any {
	tags := make(map[string]any, startTagsHint)
	addProcedureTags(tags, spec)
	if protocol != nil {
		addProtocolTags(tags, protocol, spec.Procedure)
	}
	return tags
}

// sharesStartTags reports whether the spans of spec start with the shared tags of startTags: user
// span options and custom tags may change a span's start tags, and the procedures that a client
// chose would fill the cache.
func (c *Config) sharesStartTags(spec Spec) bool {
	return len(c.spanOpts) == 0 && c.customTags == nil && !spec.Unrecognized
}

// startTags returns an option that sets every start tag of a span whose base is base: the tags of
// spec and of protocol unless it is nil, the base's and the service. The spans of a base, spec and
// protocol share the option and its tag map while the service is unchanged: when no earlier option
// has set tags, the option hands that map to the span's config, which the tracer only reads. The
// caller passes no later option that sets tags. ok is false if the spans of spec don't share start
// tags (see sharesStartTags), or if the cache is full and holds no option for them: they then
// build their own, which costs less than an option that is not cached.
func (c *Config) startTags(base *tracer.StartSpanConfig, spec Spec, protocol *Protocol) (opt tracer.StartSpanOption, ok bool) {
	if !c.sharesStartTags(spec) {
		return nil, false
	}
	key := startKey{base: base, spec: spec, protocol: protocol}
	cached, ok := c.starts.load(key)
	if !ok && c.starts.full() {
		return nil, false
	}
	svc := c.resolveService()
	if ok && cached.svc == svc {
		return cached.opt, true
	}
	tags := dynamicTags(spec, protocol)
	maps.Copy(tags, base.Tags)
	tags[ext.KeyServiceSource] = svc.override
	start := &cachedStart{svc: svc, opt: tracer.WithStartSpanConfig(&tracer.StartSpanConfig{Tags: tags})}
	// A cached option is replaced when the default service changes before the tracer is
	// initialized.
	c.starts.store(key, start, ok)
	return start.opt, true
}

// startKey identifies the spans that start with the same tags apart from the service.
type startKey struct {
	base     *tracer.StartSpanConfig
	spec     Spec
	protocol *Protocol
}

// cachedStart is the start option of the spans of a startKey with the service svc.
type cachedStart struct {
	svc *resolvedService
	opt tracer.StartSpanOption
}

// maxCachedStarts bounds the start options that a Config caches, about 1 KB each, as a client may
// call procedures that its own callers chose.
const maxCachedStarts = 1000

// startCache caches the start options of startTags.
type startCache struct {
	starts sync.Map // startKey -> *cachedStart
	size   atomic.Int32
}

// full reports whether the cache holds as many options as it may.
func (s *startCache) full() bool {
	return s.size.Load() >= maxCachedStarts
}

// cachesOrHasRoom reports whether the cache holds an option for key, or has room for one.
func (s *startCache) cachesOrHasRoom(key startKey) bool {
	if _, ok := s.starts.Load(key); ok {
		return true
	}
	return !s.full()
}

func (s *startCache) load(key startKey) (*cachedStart, bool) {
	cached, ok := s.starts.Load(key)
	if !ok {
		return nil, false
	}
	return cached.(*cachedStart), true
}

// store caches start for key, replacing the option of key if replace is set. Otherwise, it caches
// nothing once the cache is full, and keeps the option that another span cached first.
func (s *startCache) store(key startKey, start *cachedStart, replace bool) {
	switch {
	case replace:
		s.starts.Store(key, start)
	case s.size.Add(1) > maxCachedStarts:
		s.size.Add(-1)
	default:
		if _, loaded := s.starts.LoadOrStore(key, start); loaded {
			s.size.Add(-1)
		}
	}
}

// callTags hands tags, which the caller allocated for this one call span, to the tracer as its
// start tags: unlike tracer.WithTags, it neither snapshots nor copies them. Like WithTags, it must
// precede the cached base, which is then merged into tags.
func callTags(tags map[string]any) tracer.StartSpanOption {
	return func(cfg *tracer.StartSpanConfig) {
		if cfg.Tags == nil {
			cfg.Tags = tags
			return
		}
		maps.Copy(cfg.Tags, tags)
	}
}

// messageTags returns the dynamic tags shared by every message span of a stream.
func (c *Config) messageTags(spec Spec, protocol *Protocol) tracer.StartSpanOption {
	tags := make(map[string]any, 6)
	addProcedureTags(tags, spec)
	addProtocolTags(tags, protocol, spec.Procedure)
	return tracer.WithTags(tags)
}

// startTaggedMessageSpan starts a message span on side with a stream's messageTags and parentOpts,
// and sets peer's tags on it. Root-ness is checked on the started span because the user's span
// options may set a parent; callers must not share an extracted parent between message spans,
// since the tracer backfills it the first time it parents a span. Like call spans, message spans
// always get the service option, even an empty one: without a call span, they then don't inherit
// the service of a local parent such as an HTTP server span, and under one, the tracer maps their
// service once instead of mapping the call span's mapped service again.
func (c *Config) startTaggedMessageSpan(ctx context.Context, tags tracer.StartSpanOption, side instrumentation.Component, peer PeerTags, parentOpts ...tracer.StartSpanOption) *tracer.Span {
	var buf [8]tracer.StartSpanOption
	return c.startMessageSpan(ctx, side, peer, c.appendSpanOptions(buf[:0], tags, parentOpts, c.bases(side).messageOpt, c.resolveService().opt))
}

// startMessageSpan starts a message span on side with opts, and sets peer's tags on it.
func (c *Config) startMessageSpan(ctx context.Context, side instrumentation.Component, peer PeerTags, opts []tracer.StartSpanOption) *tracer.Span {
	span, _ := tracer.StartSpanFromContext(ctx, operationMessage, opts...)
	if span.Root() == span {
		span.SetTag(ext.SpanKind, c.bases(side).spanKind)
	}
	peer.Set(span)
	return span
}

// messageSpans starts the message spans of a stream on one side.
type messageSpans struct {
	cfg      *Config
	ctx      context.Context
	side     instrumentation.Component
	spec     Spec
	protocol *Protocol
	// tags are the stream's messageTags when its spans don't share cached start tags.
	tags tracer.StartSpanOption
}

func (m *messageSpans) init(ctx context.Context, cfg *Config, side instrumentation.Component, spec Spec, protocol *Protocol) {
	m.cfg, m.ctx, m.side, m.spec, m.protocol = cfg, ctx, side, spec, protocol
	if !cfg.sharesStartTags(spec) || !cfg.starts.cachesOrHasRoom(startKey{base: cfg.bases(side).message, spec: spec, protocol: protocol}) {
		m.tags = cfg.messageTags(spec, protocol)
	}
}

// start starts a message span, like startTaggedMessageSpan.
func (m *messageSpans) start(peer PeerTags, parentOpts ...tracer.StartSpanOption) *tracer.Span {
	tags := m.tags
	if tags == nil {
		if start, ok := m.cfg.startTags(m.cfg.bases(m.side).message, m.spec, m.protocol); ok {
			var buf [8]tracer.StartSpanOption
			return m.cfg.startMessageSpan(m.ctx, m.side, peer, append(append(buf[:0], start), parentOpts...))
		}
		// The cache filled up after init.
		tags = m.cfg.messageTags(m.spec, m.protocol)
	}
	return m.cfg.startTaggedMessageSpan(m.ctx, tags, m.side, peer, parentOpts...)
}

// ClientMessages starts the message spans of a client stream. The stream wrapper holds it in a
// field, and calls Init before Start.
type ClientMessages struct {
	spans messageSpans
	peer  PeerTags
}

// Init prepares m for the messages of an RPC of spec over protocol, whose stream has the context
// ctx and a peer with the tags peer.
func (m *ClientMessages) Init(ctx context.Context, cfg *Config, spec Spec, protocol *Protocol, peer PeerTags) {
	m.spans.init(ctx, cfg, instrumentation.ComponentClient, spec, protocol)
	m.peer = peer
}

// Start starts a message span of the stream and sets the peer's tags on it.
func (m *ClientMessages) Start() *tracer.Span {
	return m.spans.start(m.peer)
}

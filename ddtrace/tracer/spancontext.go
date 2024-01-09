// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/internal/tracerstats"
	ginternal "github.com/DataDog/dd-trace-go/v2/internal"
	sharedinternal "github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/samplernames"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

const TraceIDZero string = "00000000000000000000000000000000"

var _ ddtrace.SpanContext = (*SpanContext)(nil)

type traceID [16]byte // traceID in big endian, i.e. <upper><lower>

var emptyTraceID traceID

func (t *traceID) HexEncoded() string {
	return hex.EncodeToString(t[:])
}

func (t *traceID) Lower() uint64 {
	return binary.BigEndian.Uint64(t[8:])
}

func (t *traceID) Upper() uint64 {
	return binary.BigEndian.Uint64(t[:8])
}

func (t *traceID) SetLower(i uint64) {
	binary.BigEndian.PutUint64(t[8:], i)
}

func (t *traceID) SetUpper(i uint64) {
	binary.BigEndian.PutUint64(t[:8], i)
}

func (t *traceID) SetUpperFromHex(s string) error {
	u, err := strconv.ParseUint(s, 16, 64)
	if err != nil {
		return fmt.Errorf("malformed %q: %s", s, err)
	}
	t.SetUpper(u)
	return nil
}

func (t *traceID) Empty() bool {
	return *t == emptyTraceID
}

func (t *traceID) HasUpper() bool {
	for _, b := range t[:8] {
		if b != 0 {
			return true
		}
	}
	return false
}

func (t *traceID) UpperHex() string {
	return hex.EncodeToString(t[:8])
}

// SpanContext represents a span state that can propagate to descendant spans
// and across process boundaries. It contains all the information needed to
// spawn a direct descendant of the span that it belongs to. It can be used
// to create distributed tracing by propagating it using the provided interfaces.
type SpanContext struct {
	updated bool // updated is tracking changes for priority / origin / x-datadog-tags

	// the below group should propagate only locally

	trace  *trace // reference to the trace that this span belongs too
	span   *Span  // reference to the span that hosts this context
	errors int32  // number of spans with errors in this trace

	// the below group should propagate cross-process

	traceID traceID
	spanID  uint64

	mu         sync.RWMutex // guards below fields
	baggage    map[string]string
	hasBaggage uint32 // atomic int for quick checking presence of baggage. 0 indicates no baggage, otherwise baggage exists.
	origin     string // e.g. "synthetics"
}

// FromGenericCtx converts a ddtrace.SpanContext to a *SpanContext, which can be used
// to start child spans.
func FromGenericCtx(c ddtrace.SpanContext) *SpanContext {
	var sc SpanContext
	sc.traceID = c.TraceIDBytes()
	sc.spanID = c.SpanID()
	sc.baggage = make(map[string]string)
	c.ForeachBaggageItem(func(k, v string) bool {
		sc.hasBaggage = 1
		sc.baggage[k] = v
		return true
	})
	return &sc
}

// newSpanContext creates a new SpanContext to serve as context for the given
// span. If the provided parent is not nil, the context will inherit the trace,
// baggage and other values from it. This method also pushes the span into the
// new context's trace and as a result, it should not be called multiple times
// for the same span.
func newSpanContext(span *Span, parent *SpanContext) *SpanContext {
	context := &SpanContext{
		spanID: span.spanID,
		span:   span,
	}
	context.traceID.SetLower(span.traceID)
	if parent != nil {
		context.traceID.SetUpper(parent.traceID.Upper())
		context.trace = parent.trace
		context.origin = parent.origin
		context.errors = parent.errors
		parent.ForeachBaggageItem(func(k, v string) bool {
			context.setBaggageItem(k, v)
			return true
		})
	} else if sharedinternal.BoolEnv("DD_TRACE_128_BIT_TRACEID_GENERATION_ENABLED", true) {
		// add 128 bit trace id, if enabled, formatted as big-endian:
		// <32-bit unix seconds> <32 bits of zero> <64 random bits>
		id128 := time.Duration(span.start) / time.Second
		// casting from int64 -> uint32 should be safe since the start time won't be
		// negative, and the seconds should fit within 32-bits for the foreseeable future.
		// (We only want 32 bits of time, then the rest is zero)
		tUp := uint64(uint32(id128)) << 32 // We need the time at the upper 32 bits of the uint
		context.traceID.SetUpper(tUp)
	}
	if context.trace == nil {
		context.trace = newTrace()
	}
	if context.trace.root == nil {
		// first span in the trace can safely be assumed to be the root
		context.trace.root = span
	}
	// put span in context's trace
	context.trace.push(span)
	// setting context.updated to false here is necessary to distinguish
	// between initializing properties of the span (priority)
	// and updating them after extracting context through propagators
	context.updated = false
	return context
}

// SpanID implements ddtrace.SpanContext.
func (c *SpanContext) SpanID() uint64 { return c.spanID }

// TraceID implements ddtrace.SpanContext.
func (c *SpanContext) TraceID() string {
	return c.traceID.HexEncoded()
}

// TraceIDBytes implements ddtrace.SpanContext.
func (c *SpanContext) TraceIDBytes() [16]byte {
	return c.traceID
}

// ForeachBaggageItem implements ddtrace.SpanContext.
func (c *SpanContext) ForeachBaggageItem(handler func(k, v string) bool) {
	if atomic.LoadUint32(&c.hasBaggage) == 0 {
		return
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	for k, v := range c.baggage {
		if !handler(k, v) {
			break
		}
	}
}

func (c *SpanContext) setSamplingPriority(p int, sampler samplernames.SamplerName) {
	if c.trace == nil {
		c.trace = newTrace()
	}
	if c.trace.setSamplingPriority(p, sampler) {
		// the trace's sampling priority was updated: mark this as updated
		c.updated = true
	}
}

func (c *SpanContext) SamplingPriority() (p int, ok bool) {
	if c == nil || c.trace == nil {
		return 0, false
	}
	return c.trace.samplingPriority()
}

func (c *SpanContext) setBaggageItem(key, val string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.baggage == nil {
		atomic.StoreUint32(&c.hasBaggage, 1)
		c.baggage = make(map[string]string, 1)
	}
	c.baggage[key] = val
}

func (c *SpanContext) baggageItem(key string) string {
	if atomic.LoadUint32(&c.hasBaggage) == 0 {
		return ""
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.baggage[key]
}

func (c *SpanContext) meta(key string) (val string, ok bool) {
	c.span.RLock()
	defer c.span.RUnlock()
	val, ok = c.span.meta[key]
	return val, ok
}

// finish marks this span as finished in the trace.
func (c *SpanContext) finish() { c.trace.finishedOne(c.span) }

// samplingDecision is the decision to send a trace to the agent or not.
type samplingDecision uint32

const (
	// decisionNone is the default state of a trace.
	// If no decision is made about the trace, the trace won't be sent to the agent.
	decisionNone samplingDecision = iota
	// decisionDrop prevents the trace from being sent to the agent.
	decisionDrop
	// decisionKeep ensures the trace will be sent to the agent.
	decisionKeep
)

// trace contains shared context information about a trace, such as sampling
// priority, the root reference and a buffer of the spans which are part of the
// trace, if these exist.
type trace struct {
	mu               sync.RWMutex      // guards below fields
	spans            []*Span           // all the spans that are part of this trace
	tags             map[string]string // trace level tags
	propagatingTags  map[string]string // trace level tags that will be propagated across service boundaries
	finished         int               // the number of finished spans
	full             bool              // signifies that the span buffer is full
	priority         *float64          // sampling priority
	locked           bool              // specifies if the sampling priority can be altered
	samplingDecision samplingDecision  // samplingDecision indicates whether to send the trace to the agent.

	// root specifies the root of the trace, if known; it is nil when a span
	// context is extracted from a carrier, at which point there are no spans in
	// the trace yet.
	root *Span
}

var (
	// traceStartSize is the initial size of our trace buffer,
	// by default we allocate for a handful of spans within the trace,
	// reasonable as span is actually way bigger, and avoids re-allocating
	// over and over. Could be fine-tuned at runtime.
	traceStartSize = 10
	// traceMaxSize is the maximum number of spans we keep in memory for a
	// single trace. This is to avoid memory leaks. If more spans than this
	// are added to a trace, then the trace is dropped and the spans are
	// discarded. Adding additional spans after a trace is dropped does
	// nothing.
	traceMaxSize = int(1e5)
)

// newTrace creates a new trace using the given callback which will be called
// upon completion of the trace.
func newTrace() *trace {
	return &trace{spans: make([]*Span, 0, traceStartSize)}
}

func (t *trace) samplingPriorityLocked() (p int, ok bool) {
	if t.priority == nil {
		return 0, false
	}
	return int(*t.priority), true
}

func (t *trace) samplingPriority() (p int, ok bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.samplingPriorityLocked()
}

// setSamplingPriority sets the sampling priority and returns true if it was modified.
func (t *trace) setSamplingPriority(p int, sampler samplernames.SamplerName) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.setSamplingPriorityLocked(p, sampler)
}

func (t *trace) keep() {
	atomic.CompareAndSwapUint32((*uint32)(&t.samplingDecision), uint32(decisionNone), uint32(decisionKeep))
}

func (t *trace) drop() {
	atomic.CompareAndSwapUint32((*uint32)(&t.samplingDecision), uint32(decisionNone), uint32(decisionDrop))
}

func (t *trace) setTag(key, value string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.setTagLocked(key, value)
}

func (t *trace) setTagLocked(key, value string) {
	if t.tags == nil {
		t.tags = make(map[string]string, 1)
	}
	t.tags[key] = value
}

func (t *trace) setSamplingPriorityLocked(p int, sampler samplernames.SamplerName) bool {
	if t.locked {
		return false
	}

	updatedPriority := t.priority == nil || *t.priority != float64(p)

	if t.priority == nil {
		t.priority = new(float64)
	}
	*t.priority = float64(p)
	_, ok := t.propagatingTags[keyDecisionMaker]
	if p > 0 && !ok && sampler != samplernames.Unknown {
		// We have a positive priority and the sampling mechanism isn't set.
		// Send nothing when sampler is `Unknown` for RFC compliance.
		t.setPropagatingTagLocked(keyDecisionMaker, "-"+strconv.Itoa(int(sampler)))
	}
	if p <= 0 && ok {
		delete(t.propagatingTags, keyDecisionMaker)
	}

	return updatedPriority
}

func (t *trace) isLocked() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.locked
}

func (t *trace) setLocked(locked bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.locked = locked
}

// push pushes a new span into the trace. If the buffer is full, it returns
// a errBufferFull error.
func (t *trace) push(sp *Span) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.full {
		return
	}
	tr := GetGlobalTracer()
	if len(t.spans) >= traceMaxSize {
		// capacity is reached, we will not be able to complete this trace.
		t.full = true
		t.spans = nil // allow our spans to be collected by GC.
		log.Error("trace buffer full (%d spans), dropping trace", traceMaxSize)
		if tr != nil {
			tracerstats.Signal(tracerstats.TracesDropped, 1)
		}
		return
	}
	if v, ok := sp.metrics[keySamplingPriority]; ok {
		t.setSamplingPriorityLocked(int(v), samplernames.Unknown)
	}
	t.spans = append(t.spans, sp)
	if tr != nil {
		tracerstats.Signal(tracerstats.SpanStarted, 1)
	}
}

// setTraceTags sets all "trace level" tags on the provided span
// t must already be locked.
func (t *trace) setTraceTags(s *Span) { //tr *tracer) {
	for k, v := range t.tags {
		s.setMeta(k, v)
	}
	for k, v := range t.propagatingTags {
		s.setMeta(k, v)
	}
	for k, v := range ginternal.GetTracerGitMetadataTags() {
		s.setMeta(k, v)
	}
	if s.context != nil && s.context.traceID.HasUpper() {
		s.setMeta(keyTraceID128, s.context.traceID.UpperHex())
	}
	// TODO(kjn v2): Move this into the tracer or delete it.
	// Do we still want to do this hostname junk?
	// if hn := tr.hostname(); hn != "" {
	// 	s.setMeta(keyTracerHostname, hn)
	// }
}

// finishedOne acknowledges that another span in the trace has finished, and checks
// if the trace is complete, in which case it calls the onFinish function. It uses
// the given priority, if non-nil, to mark the root span. This also will trigger a partial flush
// if enabled and the total number of finished spans is greater than or equal to the partial flush limit.
// The provided span must be locked.
func (t *trace) finishedOne(s *Span) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s.finished = true
	if t.full {
		// capacity has been reached, the buffer is no longer tracking
		// all the spans in the trace, so the below conditions will not
		// be accurate and would trigger a pre-mature flush, exposing us
		// to a race condition where spans can be modified while flushing.
		//
		// TODO(partialFlush): should we do a partial flush in this scenario?
		return
	}
	t.finished++
	tr := GetGlobalTracer()
	if tr == nil {
		return
	}
	tc := tr.TracerConf()
	setPeerService(s, tr)

	// attach the _dd.base_service tag only when the globally configured service name is different from the
	// span service name.
	if s.service != "" && !strings.EqualFold(s.service, tc.ServiceTag) {
		s.meta[keyBaseService] = tc.ServiceTag
	}
	if s == t.root && t.priority != nil {
		// after the root has finished we lock down the priority;
		// we won't be able to make changes to a span after finishing
		// without causing a race condition.
		t.root.setMetric(keySamplingPriority, *t.priority)
		t.locked = true
	}
	if len(t.spans) > 0 && s == t.spans[0] {
		// first span in chunk finished, lock down the tags
		//
		// TODO(barbayar): make sure this doesn't happen in vain when switching to
		// the new wire format. We won't need to set the tags on the first span
		// in the chunk there.
		t.setTraceTags(s)
	}

	// This is here to support the mocktracer. It would be nice to be able to not do this.
	// We need to track when any single span is finished.
	if mtr, ok := tr.(interface{ FinishSpan(*Span) }); ok {
		mtr.FinishSpan(s)
	}

	if len(t.spans) == t.finished { // perform a full flush of all spans
		t.finishChunk(tr, &Chunk{
			Spans:    t.spans,
			willSend: decisionKeep == samplingDecision(atomic.LoadUint32((*uint32)(&t.samplingDecision))),
		})
		t.spans = nil
		return
	}

	doPartialFlush := tc.PartialFlush && t.finished >= tc.PartialFlushMinSpans
	if !doPartialFlush {
		return // The trace hasn't completed and partial flushing will not occur
	}
	log.Debug("Partial flush triggered with %d finished spans", t.finished)
	telemetry.GlobalClient.Count(telemetry.NamespaceTracers, "trace_partial_flush.count", 1, []string{"reason:large_trace"}, true)
	finishedSpans := make([]*Span, 0, t.finished)
	leftoverSpans := make([]*Span, 0, len(t.spans)-t.finished)
	for _, s2 := range t.spans {
		if s2.finished {
			finishedSpans = append(finishedSpans, s2)
		} else {
			leftoverSpans = append(leftoverSpans, s2)
		}
	}
	// TODO: (Support MetricKindDist) Re-enable these when we actually support `MetricKindDist`
	//telemetry.GlobalClient.Record(telemetry.NamespaceTracers, telemetry.MetricKindDist, "trace_partial_flush.spans_closed", float64(len(finishedSpans)), nil, true)
	//telemetry.GlobalClient.Record(telemetry.NamespaceTracers, telemetry.MetricKindDist, "trace_partial_flush.spans_remaining", float64(len(leftoverSpans)), nil, true)
	finishedSpans[0].setMetric(keySamplingPriority, *t.priority)
	if s != t.spans[0] {
		// Make sure the first span in the chunk has the trace-level tags
		t.setTraceTags(finishedSpans[0])
	}
	t.finishChunk(tr, &Chunk{
		Spans:    finishedSpans,
		willSend: decisionKeep == samplingDecision(atomic.LoadUint32((*uint32)(&t.samplingDecision))),
	})
	t.spans = leftoverSpans
}

func (t *trace) finishChunk(tr Tracer, ch *Chunk) {
	//atomic.AddUint32(&tr.spansFinished, uint32(len(ch.spans)))
	//tr.pushChunk(ch)
	tr.SubmitChunk(ch)
	t.finished = 0 // important, because a buffer can be used for several flushes
}

// setPeerService sets the peer.service, _dd.peer.service.source, and _dd.peer.service.remapped_from
// tags as applicable for the given span.
func setPeerService(s *Span, t Tracer) {
	tc := t.TracerConf()
	if _, ok := s.meta[ext.PeerService]; ok { // peer.service already set on the span
		s.setMeta(keyPeerServiceSource, ext.PeerService)
	} else { // no peer.service currently set
		spanKind := s.meta[ext.SpanKind]
		isOutboundRequest := spanKind == ext.SpanKindClient || spanKind == ext.SpanKindProducer
		shouldSetDefaultPeerService := isOutboundRequest && tc.PeerServiceDefaults
		if !shouldSetDefaultPeerService {
			return
		}
		source := setPeerServiceFromSource(s)
		if source == "" {
			log.Debug("No source tag value could be found for span %q, peer.service not set", s.name)
			return
		}
		s.setMeta(keyPeerServiceSource, source)
	}
	// Overwrite existing peer.service value if remapped by the user
	ps := s.meta[ext.PeerService]
	if to, ok := tc.PeerServiceMappings[ps]; ok {
		s.setMeta(keyPeerServiceRemappedFrom, ps)
		s.setMeta(ext.PeerService, to)
	}
}

// setPeerServiceFromSource sets peer.service from the sources determined
// by the tags on the span. It returns the source tag name that it used for
// the peer.service value, or the empty string if no valid source tag was available.
func setPeerServiceFromSource(s *Span) string {
	has := func(tag string) bool {
		_, ok := s.meta[tag]
		return ok
	}
	var sources []string
	useTargetHost := true
	switch {
	// order of the cases and their sources matters here. These are in priority order (highest to lowest)
	case has("aws_service"):
		sources = []string{
			"queuename",
			"topicname",
			"streamname",
			"tablename",
			"bucketname",
		}
	case s.meta[ext.DBSystem] == ext.DBSystemCassandra:
		sources = []string{
			ext.CassandraContactPoints,
		}
		useTargetHost = false
	case has(ext.DBSystem):
		sources = []string{
			ext.DBName,
			ext.DBInstance,
		}
	case has(ext.MessagingSystem):
		sources = []string{
			ext.KafkaBootstrapServers,
		}
	case has(ext.RPCSystem):
		sources = []string{
			ext.RPCService,
		}
	}
	// network destination tags will be used as fallback unless there are higher priority sources already set.
	if useTargetHost {
		sources = append(sources, []string{
			ext.NetworkDestinationName,
			ext.PeerHostname,
			ext.TargetHost,
		}...)
	}
	for _, source := range sources {
		if val, ok := s.meta[source]; ok {
			s.setMeta(ext.PeerService, val)
			return source
		}
	}
	return ""
}

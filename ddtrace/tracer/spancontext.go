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

	"github.com/trailofbits/go-mutexasserts"

	"github.com/DataDog/dd-trace-go/v2/ddtrace"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/internal/tracerstats"
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
	// TODO(kakkoyun): Check unguarded fields are safe to access.

	updated bool // updated is tracking changes for priority / origin / x-datadog-tags

	// the below group should propagate only locally

	trace *trace // reference to the trace that this span belongs too
	span  *Span  // reference to the span that hosts this context
	// +checkatomic
	errors int32 // number of spans with errors in this trace

	// The 16-character hex string of the last seen Datadog Span ID
	// this value will be added as the _dd.parent_id tag to spans
	// created from this spanContext.
	// This value is extracted from the `p` sub-key within the tracestate.
	// The backend will use the _dd.parent_id tag to reparent spans in
	// distributed traces if they were missing their parent span.
	// Missing parent span could occur when a W3C-compliant tracer
	// propagated this context, but didn't send any spans to Datadog.
	reparentID string
	isRemote   bool

	// the below group should propagate cross-process

	traceID traceID
	spanID  uint64

	mu sync.RWMutex // guards below fields

	// +checklocks:mu
	baggage map[string]string
	// +checkatomic
	hasBaggage uint32 // atomic int for quick checking presence of baggage. 0 indicates no baggage, otherwise baggage exists.
	// +checklocks:mu
	origin string // e.g. "synthetics"
	// +checklocks:mu
	spanLinks []SpanLink // links to related spans in separate|external|disconnected traces
}

// Private interface for converting v1 span contexts to v2 ones.
type spanContextV1Adapter interface {
	SamplingDecision() uint32
	Origin() string
	Priority() *float64
	PropagatingTags() map[string]string
	Tags() map[string]string
}

// FromGenericCtx converts a ddtrace.SpanContext to a *SpanContext, which can be used
// to start child spans.
// +checklocksignore: mu is locked for ForeachBaggageItem.
func FromGenericCtx(c ddtrace.SpanContext) *SpanContext {
	var sc SpanContext
	sc.traceID = c.TraceIDBytes()
	sc.spanID = c.SpanID()
	sc.baggage = make(map[string]string)
	c.ForeachBaggageItem(func(k, v string) bool {
		atomic.StoreUint32(&sc.hasBaggage, 1)
		sc.baggage[k] = v // +checklocksignore: mu is locked for ForeachBaggageItem.
		return true
	})
	ctx, ok := c.(spanContextV1Adapter)
	if !ok {
		return &sc
	}
	sc.origin = ctx.Origin()
	sc.trace = newTrace()
	// +checklocksforce: sc is just getting allocated.
	sc.trace.priority = ctx.Priority()
	atomic.StoreUint32((*uint32)(&sc.trace.samplingDecision), uint32(ctx.SamplingDecision()))
	sc.trace.tags = ctx.Tags()
	sc.trace.propagatingTags = ctx.PropagatingTags()
	return &sc
}

// newSpanContext creates a new SpanContext to serve as context for the given
// span. If the provided parent is not nil, the context will inherit the trace,
// baggage and other values from it. This method also pushes the span into the
// new context's trace and as a result, it should not be called multiple times
// for the same span.
// +checklocksread:span.mu
func newSpanContext(span *Span, parent *SpanContext) *SpanContext {
	mutexasserts.AssertRWMutexRLocked(&span.mu)

	context := &SpanContext{
		spanID: span.spanID,
		span:   span,
	}

	context.traceID.SetLower(span.traceID)
	if parent != nil {
		context.traceID.SetUpper(parent.traceID.Upper())
		context.trace = parent.trace
		context.setOrigin(parent.getOrigin())
		atomic.StoreInt32(&context.errors, atomic.LoadInt32(&parent.errors))
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
func (c *SpanContext) SpanID() uint64 {
	if c == nil {
		return 0
	}
	return c.spanID
}

func (c *SpanContext) getOrigin() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.origin
}

func (c *SpanContext) setOrigin(origin string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.origin = origin
}

func (c *SpanContext) getSpanLinks() []SpanLink {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.spanLinks
}

func (c *SpanContext) setSpanLinks(spanLinks []SpanLink) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.spanLinks = spanLinks
}

// TraceID implements ddtrace.SpanContext.
func (c *SpanContext) TraceID() string {
	if c == nil {
		return TraceIDZero
	}
	return c.traceID.HexEncoded()
}

// TraceIDBytes implements ddtrace.SpanContext.
func (c *SpanContext) TraceIDBytes() [16]byte {
	if c == nil {
		return emptyTraceID
	}
	return c.traceID
}

// TraceIDLower implements ddtrace.SpanContext.
func (c *SpanContext) TraceIDLower() uint64 {
	if c == nil {
		return 0
	}
	return c.traceID.Lower()
}

// TraceIDUpper implements ddtrace.SpanContext.
func (c *SpanContext) TraceIDUpper() uint64 {
	if c == nil {
		return 0
	}
	return c.traceID.Upper()
}

// SpanLinks implements ddtrace.SpanContext
func (c *SpanContext) SpanLinks() []SpanLink {
	c.mu.RLock()
	defer c.mu.RUnlock()
	cp := make([]SpanLink, len(c.spanLinks))
	copy(cp, c.spanLinks)
	return cp
}

// ForeachBaggageItem implements ddtrace.SpanContext.
func (c *SpanContext) ForeachBaggageItem(handler func(k, v string) bool) {
	if c == nil {
		return
	}
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

// sets the sampling priority and decision maker (based on `sampler`).
func (c *SpanContext) setSamplingPriority(p int, sampler samplernames.SamplerName) {
	if c.trace == nil {
		c.trace = newTrace()
	}
	if c.trace.setSamplingPriority(p, sampler) {
		// the trace's sampling priority or sampler was updated: mark this as updated
		c.updated = true
	}
}

func (c *SpanContext) SamplingPriority() (p int, ok bool) {
	if c == nil || c.trace == nil {
		return 0, false
	}
	return c.trace.samplingPriority()
}

func (c *SpanContext) getBaggage() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.baggage
}

func (c *SpanContext) setBaggage(baggage map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.baggage = baggage
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

// finish marks this span as finished in the trace.
// +checklocks:c.span.mu
func (c *SpanContext) finish() {
	mutexasserts.AssertRWMutexLocked(&c.span.mu)
	c.trace.finishedOne(c.span)
}

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

// TODO(kakkoyun): Refactor. Move to trace.go.

// trace contains shared context information about a trace, such as sampling
// priority, the root reference and a buffer of the spans which are part of the
// trace, if these exist.
type trace struct {
	// root specifies the root of the trace, if known; it is nil when a span
	// context is extracted from a carrier, at which point there are no spans in
	// the trace yet.
	root *Span

	mu sync.RWMutex // guards below fields

	// +checklocks:mu
	spans []*Span // all the spans that are part of this trace
	// +checklocks:mu
	tags map[string]string // trace level tags
	// +checklocks:mu
	propagatingTags map[string]string // trace level tags that will be propagated across service boundaries
	// +checklocks:mu
	finished int // the number of finished spans
	// +checklocks:mu
	full bool // signifies that the span buffer is full
	// +checklocks:mu
	priority *float64 // sampling priority
	// +checklocks:mu
	locked bool // specifies if the sampling priority can be altered
	// +checkatomic
	samplingDecision samplingDecision // samplingDecision indicates whether to send the trace to the agent.
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

func (t *trace) getSpans() []*Span {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.spans
}

func (t *trace) getPropagatingTags() map[string]string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.propagatingTags
}

func (t *trace) setPropagatingTags(propagatingTags map[string]string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.propagatingTags = propagatingTags
}

func (t *trace) samplingPriority() (p int, ok bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.samplingPriorityAssumesHoldingLock()
}

// samplingPriorityAssumesHoldingLock returns the sampling priority and true if it is set.
// +checklocksread:t.mu
func (t *trace) samplingPriorityAssumesHoldingLock() (p int, ok bool) {
	mutexasserts.AssertRWMutexLocked(&t.mu)
	if t.priority == nil {
		return 0, false
	}
	return int(*t.priority), true
}

// setSamplingPriority sets the sampling priority and the decision maker
// and returns true if it was modified.
func (t *trace) setSamplingPriority(p int, sampler samplernames.SamplerName) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.setSamplingPriorityAssumesHoldingLock(p, sampler)
}

func (t *trace) keep() {
	atomic.CompareAndSwapUint32((*uint32)(&t.samplingDecision), uint32(decisionNone), uint32(decisionKeep))
}

func (t *trace) drop() {
	atomic.CompareAndSwapUint32((*uint32)(&t.samplingDecision), uint32(decisionNone), uint32(decisionDrop))
}

func (t *trace) getTags() map[string]string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.tags
}

func (t *trace) getTag(key string) string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.tags[key]
}

func (t *trace) setTag(key, value string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.setTagAssumesHoldingLock(key, value)
}

// +checklocks:t.mu
func (t *trace) setTagAssumesHoldingLock(key, value string) {
	mutexasserts.AssertRWMutexLocked(&t.mu)
	if t.tags == nil {
		t.tags = make(map[string]string, 1)
	}
	t.tags[key] = value
}

func samplerToDM(sampler samplernames.SamplerName) string {
	return "-" + strconv.Itoa(int(sampler))
}

// +checklocks:t.mu
func (t *trace) setSamplingPriorityAssumesHoldingLock(p int, sampler samplernames.SamplerName) bool {
	mutexasserts.AssertRWMutexLocked(&t.mu)
	if t.locked {
		return false
	}

	updatedPriority := t.priority == nil || *t.priority != float64(p)

	if t.priority == nil {
		t.priority = new(float64)
	}
	*t.priority = float64(p)
	curDM, existed := t.propagatingTags[keyDecisionMaker]
	if p > 0 && sampler != samplernames.Unknown {
		// We have a positive priority and the sampling mechanism isn't set.
		// Send nothing when sampler is `Unknown` for RFC compliance.
		// If a global sampling rate is set, it was always applied first. And this call can be
		// triggered again by applying a rule sampler. The sampling priority will be the same, but
		// the decision maker will be different. So we compare the decision makers as well.
		// Note that once global rate sampling is deprecated, we no longer need to compare
		// the DMs. Sampling priority is sufficient to distinguish a change in DM.
		dm := samplerToDM(sampler)
		updatedDM := !existed || dm != curDM
		if updatedDM {
			t.setPropagatingTagAssumesHoldingLock(keyDecisionMaker, dm)
			return true
		}
	}
	if p <= 0 && existed {
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
// +checklocksread:sp.mu
func (t *trace) push(sp *Span) {
	mutexasserts.AssertRWMutexRLocked(&sp.mu)

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.full {
		return
	}
	tr := getGlobalTracer()
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
		t.setSamplingPriorityAssumesHoldingLock(int(v), samplernames.Unknown)
	}
	t.spans = append(t.spans, sp)
	if tr != nil {
		tracerstats.Signal(tracerstats.SpanStarted, 1)
	}
}

// setTraceTagsAssumesHoldingLock sets all "trace level" tags on the provided span
// +checklocks:t.mu
// +checklocks:s.mu
func (t *trace) setTraceTagsAssumesHoldingLock(s *Span) {
	mutexasserts.AssertRWMutexLocked(&s.mu)
	mutexasserts.AssertRWMutexLocked(&t.mu)

	for k, v := range t.tags {
		s.setMetaAssumesHoldingLock(k, v)
	}
	for k, v := range t.propagatingTags {
		s.setMetaAssumesHoldingLock(k, v)
	}
	for k, v := range sharedinternal.GetTracerGitMetadataTags() {
		s.setMetaAssumesHoldingLock(k, v)
	}
	if s.context != nil && s.context.traceID.HasUpper() {
		s.setMetaAssumesHoldingLock(keyTraceID128, s.context.traceID.UpperHex())
	}
}

// finishedOne acknowledges that another span in the trace has finished, and checks
// if the trace is complete, in which case it calls the onFinish function. It uses
// the given priority, if non-nil, to mark the root span. This also will trigger a partial flush
// if enabled and the total number of finished spans is greater than or equal to the partial flush limit.
// The provided span must be locked.
// +checklocks:s.mu
func (t *trace) finishedOne(s *Span) {
	t.mu.Lock()
	defer t.mu.Unlock()

	mutexasserts.AssertRWMutexLocked(&s.mu)
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
	tr := getGlobalTracer()
	if tr == nil {
		return
	}
	tc := tr.TracerConf()
	setPeerServiceAssumesHoldingLock(s, tc.PeerServiceDefaults, tc.PeerServiceMappings)

	// attach the _dd.base_service tag only when the globally configured service name is different from the
	// span service name.
	if s.service != "" && !strings.EqualFold(s.service, tc.ServiceTag) {
		s.meta[keyBaseService] = tc.ServiceTag
	}
	if s == t.root && t.priority != nil {
		// after the root has finished we lock down the priority;
		// we won't be able to make changes to a span after finishing
		// without causing a race condition.
		mutexasserts.AssertRWMutexLocked(&t.root.mu)
		// +checklocksalias:t.root.mu=s.mu
		t.root.setMetricAssumesHoldingLock(keySamplingPriority, *t.priority) // +checklocksignore
		t.locked = true
	}
	if len(t.spans) > 0 && s == t.spans[0] {
		// first span in chunk finished, lock down the tags
		//
		// TODO(barbayar): make sure this doesn't happen in vain when switching to
		// the new wire format. We won't need to set the tags on the first span
		// in the chunk there.
		t.setTraceTagsAssumesHoldingLock(s)
	}

	// This is here to support the mocktracer. It would be nice to be able to not do this.
	// We need to track when any single span is finished.
	if mtr, ok := tr.(interface{ FinishSpan(*Span) }); ok {
		mtr.FinishSpan(s)
	}

	if len(t.spans) == t.finished { // perform a full flush of all spans
		t.finishChunkAssumesHoldingLock(tr, &Chunk{
			spans:    t.spans,
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
	telemetry.Count(telemetry.NamespaceTracers, "trace_partial_flush.count", []string{"reason:large_trace"}).Submit(1)
	finishedSpans := make([]*Span, 0, t.finished)
	leftoverSpans := make([]*Span, 0, len(t.spans)-t.finished)
	for _, s2 := range t.spans {
		var isFinished bool
		if s2 == s {
			isFinished = s.finished
		} else {
			// TODO(kakkoyun): Refactor.
			s2.mu.RLock()
			isFinished = s2.finished
			s2.mu.RUnlock()
		}
		if isFinished {
			finishedSpans = append(finishedSpans, s2)
		} else {
			leftoverSpans = append(leftoverSpans, s2)
		}
	}
	telemetry.Distribution(telemetry.NamespaceTracers, "trace_partial_flush.spans_closed", nil).Submit(float64(len(finishedSpans)))
	telemetry.Distribution(telemetry.NamespaceTracers, "trace_partial_flush.spans_remaining", nil).Submit(float64(len(leftoverSpans)))

	firstFinishedSpan := finishedSpans[0]
	// TODO(kakkoyun): Refactor.
	firstFinishedSpan.mu.Lock()
	firstFinishedSpan.setMetricAssumesHoldingLock(keySamplingPriority, *t.priority) // +checklocksignore
	firstFinishedSpan.mu.Unlock()
	if s != t.spans[0] {
		// Make sure the first span in the chunk has the trace-level tags.
		// TODO(kakkoyun): Refactor.
		firstFinishedSpan.mu.Lock()
		t.setTraceTagsAssumesHoldingLock(firstFinishedSpan)
		firstFinishedSpan.mu.Unlock()
	}
	t.finishChunkAssumesHoldingLock(tr, &Chunk{
		spans:    finishedSpans,
		willSend: decisionKeep == samplingDecision(atomic.LoadUint32((*uint32)(&t.samplingDecision))),
	})
	t.spans = leftoverSpans
}

// +checklocks:t.mu
func (t *trace) finishChunkAssumesHoldingLock(tr Tracer, ch *Chunk) {
	mutexasserts.AssertRWMutexLocked(&t.mu)
	tr.SubmitChunk(ch)
	t.finished = 0 // important, because a buffer can be used for several flushes
}

// TODO(kakkoyun): Refactor. Move to span.go.
// setPeerServiceAssumesHoldingLock sets the peer.service, _dd.peer.service.source, and _dd.peer.service.remapped_from
// tags as applicable for the given span.
// +checklocks:s.mu
func setPeerServiceAssumesHoldingLock(s *Span, peerServiceDefaults bool, peerServiceMappings map[string]string) {
	mutexasserts.AssertRWMutexLocked(&s.mu)

	if _, ok := s.meta[ext.PeerService]; ok { // peer.service already set on the span
		s.setMetaAssumesHoldingLock(keyPeerServiceSource, ext.PeerService)
	} else { // no peer.service currently set
		spanKind := s.meta[ext.SpanKind]
		isOutboundRequest := spanKind == ext.SpanKindClient || spanKind == ext.SpanKindProducer
		shouldSetDefaultPeerService := isOutboundRequest && peerServiceDefaults
		if !shouldSetDefaultPeerService {
			return
		}
		source := setPeerServiceFromSourceAssumesHoldingLock(s)
		if source == "" {
			log.Debug("No source tag value could be found for span %q, peer.service not set", s.name)
			return
		}
		s.setMetaAssumesHoldingLock(keyPeerServiceSource, source)
	}
	// Overwrite existing peer.service value if remapped by the user
	ps := s.meta[ext.PeerService]
	if to, ok := peerServiceMappings[ps]; ok {
		s.setMetaAssumesHoldingLock(keyPeerServiceRemappedFrom, ps)
		s.setMetaAssumesHoldingLock(ext.PeerService, to)
	}
}

// TODO(kakkoyun): Refactor. Move to span.go.
// setPeerServiceFromSourceAssumesHoldingLock sets peer.service from the sources determined
// by the tags on the span. It returns the source tag name that it used for
// the peer.service value, or the empty string if no valid source tag was available.
// +checklocks:s.mu
func setPeerServiceFromSourceAssumesHoldingLock(s *Span) string {
	mutexasserts.AssertRWMutexLocked(&s.mu)

	has := func(tag string) bool {
		_, ok := s.meta[tag]
		return ok // +checklocksignore
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
			s.setMetaAssumesHoldingLock(ext.PeerService, val)
			return source
		}
	}
	return ""
}

const hexEncodingDigits = "0123456789abcdef"

// spanIDHexEncoded returns the hex encoded string of the given span ID `u`
// with the given padding.
//
// Code is borrowed from `fmt.fmtInteger` in the standard library.
func spanIDHexEncoded(u uint64, padding int) string {
	// The allocated intbuf with a capacity of 68 bytes
	// is large enough for integer formatting.
	var intbuf [68]byte
	buf := intbuf[0:]
	if padding > 68 {
		buf = make([]byte, padding)
	}
	// Because printing is easier right-to-left: format u into buf, ending at buf[i].
	i := len(buf)
	for u >= 16 {
		i--
		buf[i] = hexEncodingDigits[u&0xF]
		u >>= 4
	}
	i--
	buf[i] = hexEncodingDigits[u]
	for i > 0 && padding > len(buf)-i {
		i--
		buf[i] = '0'
	}
	return string(buf[i:])
}

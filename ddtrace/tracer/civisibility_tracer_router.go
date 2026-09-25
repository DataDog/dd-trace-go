// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package tracer

import (
	"maps"
	"sync/atomic"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/datastreams"
	"github.com/DataDog/dd-trace-go/v2/internal/locking"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/samplernames"
)

var _ Tracer = (*ciVisibilityTracerRouter)(nil)

const (
	ciVisibilityTracerTypeTag         = "_dd.civisibility.tracer_type"
	ciVisibilityTracerTypeCIApp       = "ciapp"
	ciVisibilityTracerTypeMock        = "mock"
	ciVisibilityTracerTypeApplication = "app"
)

// ciVisibilityTracerRouter keeps the CI Visibility tracer process-global and
// routes non-CI spans to a temporary mock, an application tracer, the CI tracer,
// or nowhere, in that order. The final fallback is controlled by
// DD_CIVISIBILITY_USE_NOOP_TRACER.
type ciVisibilityTracerRouter struct {
	// +checklocks:delegatesMu
	Tracer

	delegatesMu locking.RWMutex
	// +checklocks:delegatesMu
	applicationTracer Tracer
	// +checklocks:delegatesMu
	mockTracer Tracer
	// +checklocks:delegatesMu
	dropApplicationSpans bool
}

func newCIVisibilityTracerRouter(ciTracer Tracer, dropApplicationSpans bool) *ciVisibilityTracerRouter {
	return &ciVisibilityTracerRouter{
		Tracer:               ciTracer,
		dropApplicationSpans: dropApplicationSpans,
	}
}

// wrapWithCIVisibilityTracerRouter creates the opt-in variant that drops
// ordinary spans unless an application tracer or mock is active.
func wrapWithCIVisibilityTracerRouter(tracer Tracer) *ciVisibilityTracerRouter {
	return newCIVisibilityTracerRouter(tracer, true)
}

func wrapWithCiVisibilityNoopTracer(tracer Tracer) *ciVisibilityTracerRouter {
	return wrapWithCIVisibilityTracerRouter(tracer)
}

// StartSpan implements Tracer. Start options are evaluated exactly once, then
// replayed into the selected concrete tracer.
func (t *ciVisibilityTracerRouter) StartSpan(operationName string, opts ...StartSpanOption) *Span {
	cfg := new(StartSpanConfig)
	for _, opt := range opts {
		if opt != nil {
			opt(cfg)
		}
	}
	spanType := startSpanType(cfg)
	target, tracerType := t.tracerAndTypeForSpanType(spanType)
	if target == nil {
		log.Debug("CI Visibility tracer is filtering an application span, so the span will be skipped.")
		return nil
	}
	detachParent, markTrace := t.parentRouting(tracerType, cfg.Parent)
	if detachParent {
		cfg.Parent = detachedParentContext(cfg.Parent)
	}
	span := target.StartSpan(operationName, useConfig(cfg))
	if markTrace {
		setCIVisibilityTracerType(span, tracerType)
	}
	return span
}

func startSpanType(cfg *StartSpanConfig) string {
	if cfg == nil || cfg.Tags == nil {
		return ""
	}
	spanType, _ := cfg.Tags[ext.SpanType].(string)
	return spanType
}

func (t *ciVisibilityTracerRouter) tracerForSpanType(spanType string) Tracer {
	target, _ := t.tracerAndTypeForSpanType(spanType)
	return target
}

func (t *ciVisibilityTracerRouter) tracerAndTypeForSpanType(spanType string) (Tracer, string) {
	t.delegatesMu.RLock()
	ciTracer := t.Tracer
	mockTracer := t.mockTracer
	applicationTracer := t.applicationTracer
	dropApplicationSpans := t.dropApplicationSpans
	t.delegatesMu.RUnlock()
	if isCIVisibilitySpanType(spanType) {
		return ciTracer, ciVisibilityTracerTypeCIApp
	}
	if mockTracer != nil {
		return mockTracer, ciVisibilityTracerTypeMock
	}
	if applicationTracer != nil {
		return applicationTracer, ciVisibilityTracerTypeApplication
	}
	if !dropApplicationSpans {
		return ciTracer, ciVisibilityTracerTypeCIApp
	}
	return nil, ""
}

// parentRouting reports whether the parent must be detached and whether the
// resulting trace needs a routing marker. A trace which already has the same
// marker avoids rewriting it and therefore avoids an exclusive trace lock.
func (t *ciVisibilityTracerRouter) parentRouting(tracerType string, parent *SpanContext) (detach, mark bool) {
	if parent == nil || parent.trace == nil {
		return false, true
	}
	parentTracerType, fallbackSpanType := ciVisibilityRoutingForContext(parent)
	if parentTracerType == "" {
		_, parentTracerType = t.tracerAndTypeForSpanType(fallbackSpanType)
		mark = true
	}
	if parentTracerType == "" || parentTracerType == tracerType {
		return false, mark
	}
	return true, true
}

func setCIVisibilityTracerType(span *Span, tracerType string) {
	if span == nil || span.context == nil || span.context.trace == nil || tracerType == "" {
		return
	}
	span.context.trace.setTag(ciVisibilityTracerTypeTag, tracerType)
}

func ciVisibilityTracerType(t *trace) string {
	if t == nil {
		return ""
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.tags[ciVisibilityTracerTypeTag]
}

func ciVisibilityRoutingForContext(context *SpanContext) (tracerType, fallbackSpanType string) {
	if context == nil || context.trace == nil {
		return "", ""
	}
	tracerType = ciVisibilityTracerType(context.trace)
	if tracerType == "" && context.trace.root != nil {
		fallbackSpanType = context.trace.root.spanTypeForRouting()
	}
	return tracerType, fallbackSpanType
}

// detachedParentContext preserves distributed-parent semantics without sharing
// the local trace buffer across CI Visibility and application transports.
func detachedParentContext(parent *SpanContext) *SpanContext {
	detached := &SpanContext{
		reparentID: parent.reparentID,
		traceID:    parent.traceID,
		spanID:     parent.spanID,
		trace:      detachedPropagationTrace(parent.trace),
	}
	detached.errors.Store(parent.errors.Load())

	parent.mu.RLock()
	detached.baggageOnly = parent.baggageOnly
	detached.origin = parent.origin
	detached.spanSnapshot = parent.spanSnapshot
	if len(parent.baggage) > 0 {
		detached.baggage = maps.Clone(parent.baggage)
		atomic.StoreUint32(&detached.hasBaggage, 1)
	}
	parent.mu.RUnlock()
	return detached
}

func detachedPropagationTrace(source *trace) *trace {
	detached := newTrace()
	if source == nil {
		return detached
	}

	state := snapshotPropagationTrace(source)
	if state.hasPriority {
		detached.setSamplingPriority(state.priority, samplernames.Unknown)
	}
	detached.mu.Lock()
	detached.locked = state.locked
	detached.dm = state.dm
	detached.tags = state.tags
	atomic.StoreUint32((*uint32)(&detached.samplingDecision), uint32(state.samplingDecision))
	if state.propagatingTags != nil {
		detached.propagatingTags.Store(state.propagatingTags)
	}
	detached.otel = state.otel
	detached.mu.Unlock()
	return detached
}

type propagationTraceState struct {
	priority         int
	hasPriority      bool
	locked           bool
	dm               uint32
	samplingDecision samplingDecision
	propagatingTags  map[string]string
	tags             map[string]string
	otel             *otelTraceState
}

func snapshotPropagationTrace(source *trace) propagationTraceState {
	source.mu.RLock()
	defer source.mu.RUnlock()
	priority, hasPriority := source.samplingPriority()

	state := propagationTraceState{
		priority:         priority,
		hasPriority:      hasPriority,
		locked:           source.locked,
		dm:               source.dm,
		samplingDecision: samplingDecision(atomic.LoadUint32((*uint32)(&source.samplingDecision))),
	}
	if propagatingTags := source.loadPropagatingTags(); propagatingTags != nil {
		state.propagatingTags = maps.Clone(propagatingTags)
	}
	if source.tags != nil {
		state.tags = maps.Clone(source.tags)
		delete(state.tags, ciVisibilityTracerTypeTag)
	}
	if source.otel != nil {
		state.otel = &otelTraceState{
			rv:                  cloneUint64(source.otel.rv),
			th:                  cloneUint64(source.otel.th),
			unknown:             source.otel.unknown,
			hasUpstreamDecision: source.otel.hasUpstreamDecision,
		}
	}
	return state
}

func cloneUint64(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

// SetCIVisibilityTracer updates the concrete tracer used for CI test events.
func (t *ciVisibilityTracerRouter) SetCIVisibilityTracer(ciTracer Tracer) bool {
	if ciTracer == nil {
		return false
	}
	var dropApplicationSpans *bool
	if router, ok := ciTracer.(*ciVisibilityTracerRouter); ok {
		concrete, drop := router.ciVisibilityRoutingConfig()
		ciTracer = concrete
		if ciTracer == nil {
			return false
		}
		dropApplicationSpans = &drop
	}
	t.delegatesMu.Lock()
	old := t.Tracer
	t.Tracer = ciTracer
	if dropApplicationSpans != nil {
		t.dropApplicationSpans = *dropApplicationSpans
	}
	t.delegatesMu.Unlock()
	if old != nil && old != ciTracer {
		old.Stop()
	}
	return true
}

// SetApplicationTracer installs the tracer used for ordinary application
// spans when no mock override is active. A nil tracer clears the delegate.
func (t *ciVisibilityTracerRouter) SetApplicationTracer(applicationTracer Tracer) bool {
	t.delegatesMu.Lock()
	old := t.applicationTracer
	t.applicationTracer = applicationTracer
	t.delegatesMu.Unlock()
	if old != nil && old != applicationTracer {
		old.Stop()
	}
	return true
}

// SetMockTracer installs a temporary override for ordinary spans. The caller
// owns the mock lifecycle, so replacing the override does not stop the old mock.
func (t *ciVisibilityTracerRouter) SetMockTracer(mockTracer Tracer) bool {
	if mockTracer == nil {
		return false
	}
	t.delegatesMu.Lock()
	t.mockTracer = mockTracer
	t.delegatesMu.Unlock()
	return true
}

// ClearMockTracer removes mockTracer only if it is still the active override.
// The router remains process-global throughout the active CI Visibility lifecycle.
func (t *ciVisibilityTracerRouter) ClearMockTracer(mockTracer Tracer) bool {
	t.delegatesMu.Lock()
	defer t.delegatesMu.Unlock()
	if t.mockTracer != mockTracer {
		return false
	}
	t.mockTracer = nil
	return true
}

func (t *ciVisibilityTracerRouter) detachApplicationTracer() Tracer {
	t.delegatesMu.Lock()
	applicationTracer := t.applicationTracer
	t.applicationTracer = nil
	t.delegatesMu.Unlock()
	return applicationTracer
}

func (t *ciVisibilityTracerRouter) detachActiveMockTracer() {
	t.delegatesMu.Lock()
	t.mockTracer = nil
	t.delegatesMu.Unlock()
}

func (t *ciVisibilityTracerRouter) detachCIVisibilityTracer() Tracer {
	t.delegatesMu.Lock()
	ciTracer := t.Tracer
	t.Tracer = &NoopTracer{}
	t.mockTracer = nil
	t.delegatesMu.Unlock()
	return ciTracer
}

func (t *ciVisibilityTracerRouter) ciVisibilityTracer() Tracer {
	t.delegatesMu.RLock()
	defer t.delegatesMu.RUnlock()
	return t.Tracer
}

func (t *ciVisibilityTracerRouter) ciVisibilityRoutingConfig() (Tracer, bool) {
	t.delegatesMu.RLock()
	defer t.delegatesMu.RUnlock()
	return t.Tracer, t.dropApplicationSpans
}

func (t *ciVisibilityTracerRouter) currentApplicationTracer() Tracer {
	t.delegatesMu.RLock()
	defer t.delegatesMu.RUnlock()
	return t.applicationTracer
}

func (t *ciVisibilityTracerRouter) currentMockTracer() Tracer {
	t.delegatesMu.RLock()
	defer t.delegatesMu.RUnlock()
	return t.mockTracer
}

// Extract implements Tracer.
func (t *ciVisibilityTracerRouter) Extract(carrier any) (*SpanContext, error) {
	if target := t.tracerForSpanType(""); target != nil {
		return target.Extract(carrier)
	}
	return nil, nil
}

// Inject implements Tracer.
func (t *ciVisibilityTracerRouter) Inject(context *SpanContext, carrier any) error {
	if target := t.tracerForContext(context); target != nil {
		return target.Inject(context, carrier)
	}
	return nil
}

func (t *ciVisibilityTracerRouter) tracerForContext(context *SpanContext) Tracer {
	tracerType, fallbackSpanType := ciVisibilityRoutingForContext(context)
	return t.TracerForTrace(tracerType, fallbackSpanType)
}

// TracerForTrace returns the current destination for the tracer type recorded
// when the local trace was created. Spans which predate the marker fall back to
// their span type. A no-op destination lets a trace whose delegate disappeared
// finish bookkeeping without being redirected to a different tracer type.
func (t *ciVisibilityTracerRouter) TracerForTrace(tracerType, fallbackSpanType string) Tracer {
	var target Tracer
	switch tracerType {
	case ciVisibilityTracerTypeCIApp:
		target = t.ciVisibilityTracer()
	case ciVisibilityTracerTypeMock:
		target = t.currentMockTracer()
	case ciVisibilityTracerTypeApplication:
		target = t.currentApplicationTracer()
	default:
		target = t.tracerForSpanType(fallbackSpanType)
	}
	if target != nil {
		return target
	}
	return NoopTracer{}
}

// Stop implements Tracer. If CI Visibility is still active and the router is
// replaced by NoopTracer, it restores itself to keep CI events routable.
func (t *ciVisibilityTracerRouter) Stop() {
	// The mock owns its own lifecycle, so detach it without stopping the
	// caller's handle.
	t.detachActiveMockTracer()
	applicationTracer := t.detachApplicationTracer()
	if applicationTracer != nil {
		applicationTracer.Stop()
	}

	state := civisibility.GetState()
	ciVisibilityActive := state == civisibility.StateInitializing || state == civisibility.StateInitialized
	if _, replacedByNoop := getGlobalTracer().(*NoopTracer); ciVisibilityActive && replacedByNoop {
		setGlobalTracer(t)
		return
	}

	if ciTracer := t.detachCIVisibilityTracer(); ciTracer != nil {
		ciTracer.Stop()
	}
}

// Reset lets the router use the internal non-stopping global-tracer handoff.
// Only an active mock delegate has resettable user-visible state.
func (t *ciVisibilityTracerRouter) Reset() {
	if resetter, ok := t.currentMockTracer().(interface{ Reset() }); ok {
		resetter.Reset()
	}
}

// TracerConf implements Tracer. Application tracing is the ordinary-span
// destination while it is active; otherwise the CI tracer remains authoritative.
func (t *ciVisibilityTracerRouter) TracerConf() TracerConf {
	if applicationTracer := t.currentApplicationTracer(); applicationTracer != nil {
		return applicationTracer.TracerConf()
	}
	if ciTracer := t.ciVisibilityTracer(); ciTracer != nil {
		return ciTracer.TracerConf()
	}
	return TracerConf{}
}

func (t *ciVisibilityTracerRouter) Flush() {
	ciTracer := t.ciVisibilityTracer()
	applicationTracer := t.currentApplicationTracer()
	mockTracer := t.currentMockTracer()
	if ciTracer != nil {
		ciTracer.Flush()
	}
	if applicationTracer != nil && applicationTracer != ciTracer {
		applicationTracer.Flush()
	}
	if mockTracer != nil && mockTracer != ciTracer && mockTracer != applicationTracer {
		mockTracer.Flush()
	}
}

// GetDataStreamsProcessor returns the active ordinary-span destination's
// processor, if any.
func (t *ciVisibilityTracerRouter) GetDataStreamsProcessor() *datastreams.Processor {
	if target, ok := t.tracerForSpanType("").(dataStreamsContainer); ok {
		return target.GetDataStreamsProcessor()
	}
	return nil
}

func isCIVisibilitySpanType(spanType string) bool {
	return spanType == constants.SpanTypeTest ||
		spanType == constants.SpanTypeTestSuite ||
		spanType == constants.SpanTypeTestModule ||
		spanType == constants.SpanTypeTestSession
}

func useConfig(config *StartSpanConfig) StartSpanOption {
	return func(cfg *StartSpanConfig) {
		if config == nil {
			return
		}

		cfg.Parent = config.Parent
		cfg.StartTime = config.StartTime
		cfg.Tags = config.Tags
		cfg.SpanID = config.SpanID
		cfg.Context = config.Context
		cfg.SpanLinks = config.SpanLinks
	}
}

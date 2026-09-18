// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package tracer

import (
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/datastreams"
	"github.com/DataDog/dd-trace-go/v2/internal/locking"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

var _ Tracer = (*ciVisibilityTracerRouter)(nil)

// ciVisibilityNoopTracer keeps the historical internal name used by the
// tracer's finished-chunk fallback without changing that general code path.
type ciVisibilityNoopTracer = ciVisibilityTracerRouter

// ciVisibilityTracerRouter keeps the CI Visibility tracer process-global and
// routes non-CI spans to a temporary mock, an application tracer, the CI tracer,
// or nowhere, in that order. The final fallback is controlled by
// DD_CIVISIBILITY_USE_NOOP_TRACER.
type ciVisibilityTracerRouter struct {
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
	cfg := NewStartSpanConfig(opts...)
	target := t.tracerForSpanType(startSpanType(cfg))
	if target == nil {
		log.Debug("CI Visibility tracer is filtering an application span, so the span will be skipped.")
		return nil
	}
	return target.StartSpan(operationName, useConfig(cfg))
}

func startSpanType(cfg *StartSpanConfig) string {
	if cfg == nil || cfg.Tags == nil {
		return ""
	}
	spanType, _ := cfg.Tags[ext.SpanType].(string)
	return spanType
}

func (t *ciVisibilityTracerRouter) tracerForSpanType(spanType string) Tracer {
	t.delegatesMu.RLock()
	ciTracer := t.Tracer
	mockTracer := t.mockTracer
	applicationTracer := t.applicationTracer
	dropApplicationSpans := t.dropApplicationSpans
	t.delegatesMu.RUnlock()
	if isCIVisibilitySpanType(spanType) {
		return ciTracer
	}
	if mockTracer != nil {
		return mockTracer
	}
	if applicationTracer != nil {
		return applicationTracer
	}
	if !dropApplicationSpans {
		return ciTracer
	}
	return nil
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
// spans when no mock override is active.
func (t *ciVisibilityTracerRouter) SetApplicationTracer(applicationTracer Tracer) bool {
	if applicationTracer == nil {
		return false
	}
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
func (t *ciVisibilityTracerRouter) ClearMockTracer(mockTracer Tracer) bool {
	t.delegatesMu.Lock()
	defer t.delegatesMu.Unlock()
	if t.mockTracer != mockTracer {
		return false
	}
	t.mockTracer = nil
	return true
}

// DetachMockTracer removes mockTracer and returns the tracer that should become
// process-global. When no routing remains necessary, ownership of the concrete
// CI tracer is transferred out of the router without stopping it.
func (t *ciVisibilityTracerRouter) DetachMockTracer(mockTracer Tracer) (Tracer, bool) {
	t.delegatesMu.Lock()
	defer t.delegatesMu.Unlock()
	if t.mockTracer != mockTracer {
		return nil, false
	}
	t.mockTracer = nil
	if t.applicationTracer != nil || t.dropApplicationSpans {
		return t, true
	}
	ciTracer := t.Tracer
	t.Tracer = &NoopTracer{}
	return ciTracer, ciTracer != nil
}

func (t *ciVisibilityTracerRouter) detachApplicationTracer() Tracer {
	t.delegatesMu.Lock()
	applicationTracer := t.applicationTracer
	t.applicationTracer = nil
	t.delegatesMu.Unlock()
	return applicationTracer
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
	if target := t.tracerForSpanType(""); target != nil {
		return target.Inject(context, carrier)
	}
	return nil
}

// FinishSpan lets the active mock record spans it created. The mock ignores
// spans absent from its open-span set, including CI and application spans.
func (t *ciVisibilityTracerRouter) FinishSpan(span *Span) {
	if span == nil {
		return
	}
	if target, ok := t.currentMockTracer().(interface{ FinishSpan(*Span) }); ok {
		target.FinishSpan(span)
	}
}

// TracerForFinishedChunk selects the current destination from the finished
// span type. It deliberately does not retain a per-span owner or registry.
func (t *ciVisibilityTracerRouter) TracerForFinishedChunk(spans []*Span) (Tracer, bool) {
	if len(spans) == 0 {
		return nil, false
	}
	target := t.tracerForSpanType(ciVisibilitySpanType(spans[0]))
	return target, target != nil
}

// +checklocksignore — This runs only for finished chunks. spanType is immutable
// after finish, and the finishing span may already hold Span.mu here.
func ciVisibilitySpanType(span *Span) string {
	if span == nil {
		return ""
	}
	return span.spanType
}

// Stop implements Tracer. Application tracing can stop while CI Visibility
// remains active; the router restores itself after package-level Stop swaps the
// global tracer to NoopTracer.
func (t *ciVisibilityTracerRouter) Stop() {
	applicationTracer := t.detachApplicationTracer()
	if applicationTracer != nil {
		applicationTracer.Stop()
	}

	state := civisibility.GetState()
	ciVisibilityActive := state == civisibility.StateInitializing || state == civisibility.StateInitialized
	if _, replacedByNoop := getGlobalTracer().(*NoopTracer); ciVisibilityActive && replacedByNoop {
		t.delegatesMu.Lock()
		if t.mockTracer == nil && !t.dropApplicationSpans {
			ciTracer := t.Tracer
			t.Tracer = &NoopTracer{}
			t.delegatesMu.Unlock()
			if ciTracer != nil {
				setGlobalTracer(ciTracer)
			}
			return
		}
		t.delegatesMu.Unlock()
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

// TracerConf implements Tracer and exposes the CI configuration while the
// router is process-global.
func (t *ciVisibilityTracerRouter) TracerConf() TracerConf {
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

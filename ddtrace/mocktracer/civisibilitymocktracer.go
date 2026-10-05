// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package mocktracer

import (
	"sync"
	"sync/atomic"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/datastreams"
)

type civisibilitymocktracer struct {
	// mock records user-created spans so tests that use mocktracer keep their existing assertions.
	mock *mocktracer

	// realMu protects real while CI Visibility startup, span creation, flush, and shutdown can overlap.
	realMu sync.RWMutex
	real   tracer.Tracer // real receives CI Visibility spans that must not be captured by mock.

	// realSpansMu protects realSpans, which tracks spans that must bypass mock FinishSpan handling.
	realSpansMu sync.Mutex
	realSpans   map[*tracer.Span]struct{}

	// isnoop disables user-facing mock behavior after Stop while allowing CI Visibility cleanup to continue.
	isnoop atomic.Bool
}

var (
	_ tracer.Tracer = (*civisibilitymocktracer)(nil)
	_ Tracer        = (*civisibilitymocktracer)(nil)
)

// newCIVisibilityMockTracer creates a mock tracer that delegates CI Visibility spans to the real tracer.
func newCIVisibilityMockTracer() *civisibilitymocktracer {
	currentTracer := getGlobalTracer()
	// Repeated mocktracer starts should unwrap the previous CI Visibility mock tracer
	// and keep its real tracer delegate instead of stacking wrappers.
	if currentCIVisibilityMockTracer, ok := currentTracer.(*civisibilitymocktracer); ok && currentCIVisibilityMockTracer != nil {
		currentTracer = currentCIVisibilityMockTracer.realTracer()
	}
	return &civisibilitymocktracer{
		mock:      newMockTracer(),
		real:      currentTracer,
		realSpans: make(map[*tracer.Span]struct{}),
	}
}

// realTracer returns the currently installed CI Visibility tracer delegate.
func (t *civisibilitymocktracer) realTracer() tracer.Tracer {
	t.realMu.RLock()
	defer t.realMu.RUnlock()
	return t.real
}

// SetCIVisibilityTracer installs the tracer used for CI Visibility spans while
// keeping this mock tracer as the process global tracer.
func (t *civisibilitymocktracer) SetCIVisibilityTracer(real tracer.Tracer) bool {
	if real == nil {
		return false
	}

	t.realMu.Lock()
	if t.isnoop.Load() {
		old := t.real
		t.real = &tracer.NoopTracer{}
		t.realMu.Unlock()
		if old != nil && old != real {
			stopRealTracerDelegate(old)
		}
		return false
	}
	old := t.real
	t.real = real
	t.realMu.Unlock()

	if old != nil && old != real {
		stopRealTracerDelegate(old)
	}
	return true
}

// stopRealTracerDelegate stops a tracer owned by civisibilitymocktracer without
// letting mocktracer cleanup overwrite the process global tracer.
func stopRealTracerDelegate(real tracer.Tracer) {
	if real == nil {
		return
	}
	if mt, ok := real.(*mocktracer); ok {
		if mt.dsmProcessor != nil {
			mt.dsmProcessor.Stop()
		}
		return
	}
	real.Stop()
}

// SentDSMBacklogs returns the Data Streams Monitoring backlogs that have been sent by the mock tracer.
// If the tracer is in noop mode, it returns nil. Otherwise, it flushes the processor and returns
// all captured backlogs from the mock transport.
func (t *civisibilitymocktracer) SentDSMBacklogs() []datastreams.Backlog {
	if t.isnoop.Load() {
		return nil
	}
	t.mock.dsmProcessor.Flush()
	return t.mock.dsmTransport.backlogs
}

// Stop deactivates the CI Visibility mock tracer by setting it to noop mode and stopping
// the Data Streams Monitoring processor. If this wrapper has already been removed from
// the global tracer slot, it also stops the real delegate because CI Visibility shutdown
// can no longer reach it through the global tracer.
func (t *civisibilitymocktracer) Stop() {
	var realToStop tracer.Tracer
	removedFromGlobalTracer := getGlobalTracer() != t

	t.realMu.Lock()
	t.isnoop.Store(true)
	if removedFromGlobalTracer || civisibility.GetState() == civisibility.StateExiting {
		realToStop = t.real
		t.real = &tracer.NoopTracer{}
	}
	t.realMu.Unlock()

	t.mock.dsmProcessor.Stop()
	stopRealTracerDelegate(realToStop)
}

// StartSpan creates a new span with the given operation name and options. If the span type
// indicates it's a CI Visibility span (like a test session, module, suite, or individual test),
// it uses the real tracer to create the span. For all other spans, it uses the mock tracer.
// If the mock tracer is in noop mode, non-CI Visibility spans return nil while
// CI Visibility spans may still use the real tracer until CI Visibility exits.
func (t *civisibilitymocktracer) StartSpan(operationName string, opts ...tracer.StartSpanOption) *tracer.Span {
	var cfg tracer.StartSpanConfig
	for _, fn := range opts {
		fn(&cfg)
	}

	if isCIVisibilitySpan(cfg) {
		t.realMu.RLock()
		real := t.real
		if real != nil {
			// If the span is a CI Visibility span, use the real tracer to create it.
			realSpan := real.StartSpan(operationName, opts...)
			t.realMu.RUnlock()

			if realSpan != nil {
				t.realSpansMu.Lock()
				t.realSpans[realSpan] = struct{}{}
				t.realSpansMu.Unlock()
			}
			return realSpan
		}
		t.realMu.RUnlock()
	}

	if t.isnoop.Load() {
		return nil
	}

	// Otherwise, use the mock tracer to create it.
	return t.mock.StartSpan(operationName, opts...)
}

// isCIVisibilitySpan reports whether cfg describes a CI Visibility span that
// must bypass user-facing mocktracer storage.
func isCIVisibilitySpan(cfg tracer.StartSpanConfig) bool {
	spanType, ok := cfg.Tags[ext.SpanType]
	return ok && (spanType == constants.SpanTypeTestSession ||
		spanType == constants.SpanTypeTestModule ||
		spanType == constants.SpanTypeTestSuite ||
		spanType == constants.SpanTypeTest)
}

// FinishSpan marks mock-created spans as finished while keeping CI Visibility
// spans out of the user-facing mock span list.
func (t *civisibilitymocktracer) FinishSpan(s *tracer.Span) {
	if s == nil {
		return
	}

	t.realSpansMu.Lock()
	// Check if the span is a real span (i.e., created by the real tracer).
	_, isRealSpan := t.realSpans[s]
	t.realSpansMu.Unlock()
	if isRealSpan {
		return
	}
	if t.isnoop.Load() {
		return
	}
	t.mock.FinishSpan(s)
}

// TracerForFinishedChunk returns the current real tracer when a finished chunk
// contains CI Visibility spans created by this wrapper.
func (t *civisibilitymocktracer) TracerForFinishedChunk(spans []*tracer.Span) (tracer.Tracer, bool) {
	hasRealSpan := false
	t.realSpansMu.Lock()
	for _, s := range spans {
		if _, ok := t.realSpans[s]; ok {
			delete(t.realSpans, s)
			hasRealSpan = true
		}
	}
	t.realSpansMu.Unlock()
	if !hasRealSpan {
		return nil, false
	}

	t.realMu.RLock()
	real := t.real
	t.realMu.RUnlock()
	if real == nil {
		return nil, false
	}
	return real, true
}

// GetDataStreamsProcessor returns the Data Streams Monitoring processor used by the mock tracer.
// If the tracer is in noop mode, it returns nil. This processor is used to monitor
// and record data stream metrics.
func (t *civisibilitymocktracer) GetDataStreamsProcessor() *datastreams.Processor {
	if t.isnoop.Load() {
		return nil
	}
	return t.mock.dsmProcessor
}

// OpenSpans returns the set of started spans that have not been finished yet.
// This is useful for verifying spans are properly finished in tests.
func (t *civisibilitymocktracer) OpenSpans() []*Span {
	return t.mock.OpenSpans()
}

// FinishedSpans returns the set of spans that have been finished.
// This allows inspection of spans after they've completed for testing and verification.
func (t *civisibilitymocktracer) FinishedSpans() []*Span {
	return t.mock.FinishedSpans()
}

// Reset clears all spans (both open and finished) from the mock tracer.
// This is especially useful when running tests in a loop, where a clean state
// is desired between test iterations.
func (t *civisibilitymocktracer) Reset() {
	t.mock.Reset()
}

// Extract retrieves a SpanContext from the carrier using the mock tracer's propagator.
// If the tracer is in noop mode, it returns nil. This is used for distributed tracing
// to continue traces across process boundaries.
func (t *civisibilitymocktracer) Extract(carrier any) (*tracer.SpanContext, error) {
	if t.isnoop.Load() {
		return nil, nil
	}
	return t.mock.Extract(carrier)
}

// Inject injects the SpanContext into the carrier using the mock tracer's propagator.
// If the tracer is in noop mode, it returns nil. This is used for distributed tracing
// to propagate trace information across process boundaries.
func (t *civisibilitymocktracer) Inject(context *tracer.SpanContext, carrier any) error {
	if t.isnoop.Load() {
		return nil
	}
	return t.mock.Inject(context, carrier)
}

func (t *civisibilitymocktracer) TracerConf() tracer.TracerConf {
	t.realMu.RLock()
	defer t.realMu.RUnlock()
	if t.real == nil {
		return tracer.TracerConf{}
	}
	return t.real.TracerConf()
}

// Flush forces a flush of both the mock tracer and the real tracer.
// This ensures that all buffered spans are processed and ready for inspection.
func (t *civisibilitymocktracer) Flush() {
	t.mock.Flush()
	t.realMu.RLock()
	defer t.realMu.RUnlock()
	if t.real != nil {
		t.real.Flush()
	}
}

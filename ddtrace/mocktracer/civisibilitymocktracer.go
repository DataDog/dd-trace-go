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
	mock   *mocktracer   // mock tracer
	real   tracer.Tracer // real tracer (for the testotimization/civisibility spans)
	isnoop atomic.Bool
}

var (
	_ tracer.Tracer = (*civisibilitymocktracer)(nil)
	_ Tracer        = (*civisibilitymocktracer)(nil)

	realSpans      = make(map[*tracer.Span]bool)
	realSpansMutex sync.Mutex
)

// Creates a new CIVisibilityMockTracer that uses the mock tracer for all spans except the CIVisibility spans.
func newCIVisibilityMockTracer() *civisibilitymocktracer {
	currentTracer := getGlobalTracer()
	// let's check if the current tracer is already a civisibilitymocktracer
	// if so, we need to get the real tracer from it
	if currentCIVisibilityMockTracer, ok := currentTracer.(*civisibilitymocktracer); ok && currentCIVisibilityMockTracer != nil {
		currentTracer = currentCIVisibilityMockTracer.real
	}
	return &civisibilitymocktracer{
		mock: newMockTracer(),
		real: currentTracer,
	}
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

// Stop deactivates the CIVisibility mock tracer by setting it to noop mode and stopping
// the Data Streams Monitoring processor. This should be called when testing has finished.
func (t *civisibilitymocktracer) Stop() {
	t.isnoop.Store(true)
	t.mock.dsmProcessor.Stop()
	if civisibility.GetState() == civisibility.StateExiting {
		t.real.Stop()
		t.real = &tracer.NoopTracer{}
	}
}

// StartSpan creates a new span with the given operation name and options. If the span type
// indicates it's a CI Visibility span (like a test session, module, suite, or individual test),
// it uses the real tracer to create the span. For all other spans, it uses the mock tracer.
// If the tracer is in noop mode, it returns nil.
func (t *civisibilitymocktracer) StartSpan(operationName string, opts ...tracer.StartSpanOption) *tracer.Span {
	if t.real != nil {
		var cfg tracer.StartSpanConfig
		for _, fn := range opts {
			fn(&cfg)
		}

		if spanType, ok := cfg.Tags[ext.SpanType]; ok &&
			(spanType == constants.SpanTypeTestSession || spanType == constants.SpanTypeTestModule ||
				spanType == constants.SpanTypeTestSuite || spanType == constants.SpanTypeTest) {
			// If the span is a civisibility span, use the real tracer to create it.
			realSpan := t.real.StartSpan(operationName, opts...)
			realSpansMutex.Lock()
			defer realSpansMutex.Unlock()
			realSpans[realSpan] = true
			return realSpan
		}
	}

	if t.isnoop.Load() {
		return nil
	}

	// Otherwise, use the mock tracer to create it.
	return t.mock.StartSpan(operationName, opts...)
}

// FinishSpan marks the given span as finished in the mock tracer. This is called by spans
// when they finish, adding them to the list of finished spans for later inspection.
func (t *civisibilitymocktracer) FinishSpan(s *tracer.Span) {
	realSpansMutex.Lock()
	defer realSpansMutex.Unlock()
	// Check if the span is a real span (i.e., created by the real tracer).
	if _, isRealSpan := realSpans[s]; isRealSpan {
		delete(realSpans, s)
		return
	}
	if t.isnoop.Load() {
		return
	}
	t.mock.FinishSpan(s)
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
func (t *civisibilitymocktracer) Extract(carrier interface{}) (*tracer.SpanContext, error) {
	if t.isnoop.Load() {
		return nil, nil
	}
	return t.mock.Extract(carrier)
}

// Inject injects the SpanContext into the carrier using the mock tracer's propagator.
// If the tracer is in noop mode, it returns nil. This is used for distributed tracing
// to propagate trace information across process boundaries.
func (t *civisibilitymocktracer) Inject(context *tracer.SpanContext, carrier interface{}) error {
	if t.isnoop.Load() {
		return nil
	}
	return t.mock.Inject(context, carrier)
}

func (t *civisibilitymocktracer) TracerConf() tracer.TracerConf {
	return t.real.TracerConf()
}

// Flush forces a flush of both the mock tracer and the real tracer.
// This ensures that all buffered spans are processed and ready for inspection.
func (t *civisibilitymocktracer) Flush() {
	t.mock.Flush()
	t.real.Flush()
}

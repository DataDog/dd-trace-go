// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package mocktracer

import (
	"sync/atomic"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/constants"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/datastreams"
)

type civisibilitymocktracer struct {
	mock   *mocktracer    // mock tracer
	real   ddtrace.Tracer // real tracer (for the testotimization/civisibility spans)
	isnoop atomic.Bool
}

var _ ddtrace.Tracer = (*civisibilitymocktracer)(nil)
var _ Tracer = (*civisibilitymocktracer)(nil)

// Creates a new CIVisibilityMockTracer that uses the mock tracer for all spans except the CIVisibility spans.
func newCIVisibilityMockTracer() *civisibilitymocktracer {
	return &civisibilitymocktracer{
		mock: newMockTracer(),
		real: internal.GetGlobalTracer(),
	}
}

func (t *civisibilitymocktracer) SentDSMBacklogs() []datastreams.Backlog {
	if t.isnoop.Load() {
		return nil
	}
	t.mock.dsmProcessor.Flush()
	return t.mock.dsmTransport.backlogs
}

func (t *civisibilitymocktracer) Stop() {
	t.isnoop.Store(true)
	internal.Testing = false
	t.mock.dsmProcessor.Stop()
}

func (t *civisibilitymocktracer) StartSpan(operationName string, opts ...ddtrace.StartSpanOption) ddtrace.Span {
	if t.real != nil {
		var cfg ddtrace.StartSpanConfig
		for _, fn := range opts {
			fn(&cfg)
		}

		if spanType, ok := cfg.Tags[ext.SpanType]; ok &&
			(spanType == constants.SpanTypeTestSession || spanType == constants.SpanTypeTestModule ||
				spanType == constants.SpanTypeTestSuite || spanType == constants.SpanTypeTest) {
			// If the span is a civisibility span, use the real tracer to create it.
			return t.real.StartSpan(operationName, opts...)
		}
	}

	if t.isnoop.Load() {
		return internal.NoopSpan{}
	}

	// Otherwise, use the mock tracer to create it.
	return t.mock.StartSpan(operationName, opts...)
}

func (t *civisibilitymocktracer) GetDataStreamsProcessor() *datastreams.Processor {
	if t.isnoop.Load() {
		return nil
	}
	return t.mock.dsmProcessor
}

func (t *civisibilitymocktracer) OpenSpans() []Span {
	return t.mock.OpenSpans()
}

func (t *civisibilitymocktracer) FinishedSpans() []Span {
	return t.mock.FinishedSpans()
}

func (t *civisibilitymocktracer) Reset() {
	t.mock.Reset()
}

func (t *civisibilitymocktracer) Extract(carrier interface{}) (ddtrace.SpanContext, error) {
	if t.isnoop.Load() {
		return internal.NoopSpanContext{}, nil
	}
	return t.mock.Extract(carrier)
}

func (t *civisibilitymocktracer) Inject(context ddtrace.SpanContext, carrier interface{}) error {
	if t.isnoop.Load() {
		return nil
	}
	return t.mock.Inject(context, carrier)
}

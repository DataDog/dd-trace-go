// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal // import "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"

import (
	v2 "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
)

// SetGlobalTracer sets the global tracer to t.
func SetGlobalTracer(t ddtrace.Tracer) {
	rt := t.(TracerV2Adapter)
	v2.SetGlobalTracer(rt.Tracer)
}

// GetGlobalTracer returns the currently active tracer.
func GetGlobalTracer() ddtrace.Tracer {
	tr := v2.GetGlobalTracer()
	return TracerV2Adapter{Tracer: tr}
}

var NoopTracerV2 = TracerV2Adapter{Tracer: v2.NoopTracer{}}

var _ ddtrace.Span = (*NoopSpan)(nil)

// NoopSpan is an implementation of ddtrace.Span that is a no-op.
type NoopSpan struct{}

// SetTag implements ddtrace.Span.
func (NoopSpan) SetTag(_ string, _ interface{}) {}

// SetOperationName implements ddtrace.Span.
func (NoopSpan) SetOperationName(_ string) {}

// BaggageItem implements ddtrace.Span.
func (NoopSpan) BaggageItem(_ string) string { return "" }

// SetBaggageItem implements ddtrace.Span.
func (NoopSpan) SetBaggageItem(_, _ string) {}

// Finish implements ddtrace.Span.
func (NoopSpan) Finish(_ ...ddtrace.FinishOption) {}

// Tracer implements ddtrace.Span.
func (NoopSpan) Tracer() ddtrace.Tracer { return NoopTracerV2 }

// Context implements ddtrace.Span.
func (NoopSpan) Context() ddtrace.SpanContext { return NoopSpanContext{} }

var _ ddtrace.SpanContext = (*NoopSpanContext)(nil)

// NoopSpanContext is an implementation of ddtrace.SpanContext that is a no-op.
type NoopSpanContext struct{}

// SpanID implements ddtrace.SpanContext.
func (NoopSpanContext) SpanID() uint64 { return 0 }

// TraceID implements ddtrace.SpanContext.
func (NoopSpanContext) TraceID() uint64 { return 0 }

// ForeachBaggageItem implements ddtrace.SpanContext.
func (NoopSpanContext) ForeachBaggageItem(_ func(k, v string) bool) {}

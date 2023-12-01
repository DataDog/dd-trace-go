// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"sync/atomic"

	"github.com/DataDog/dd-trace-go/v2/ddtrace"
)

var (
	// globalTracer stores the current tracer as *ddtrace.Tracer (pointer to interface). The
	// atomic.Value type requires types to be consistent, which requires using *ddtrace.Tracer.
	globalTracer atomic.Value
)

func init() {
	var tracer Tracer = &NoopTracer{}
	globalTracer.Store(&tracer)
}

// SetGlobalTracer sets the global tracer to t.
func SetGlobalTracer(t Tracer) {
	old := *globalTracer.Swap(&t).(*Tracer)
	if !Testing {
		old.Stop()
	}
}

// GetGlobalTracer returns the currently active tracer.
func GetGlobalTracer() Tracer {
	return *globalTracer.Load().(*Tracer)
}

// Testing is set to true when the mock tracer is active. It usually signifies that we are in a test
// environment. This value is used by tracer.Start to prevent overriding the GlobalTracer in tests.
var Testing = false

var _ Tracer = (*NoopTracer)(nil)

// NoopTracer is an implementation of Tracer that is a no-op.
type NoopTracer struct{}

// StartSpan implements Tracer.
func (NoopTracer) StartSpan(_ string, _ ...ddtrace.StartSpanOption) *Span {
	return nil
}

// SetServiceInfo implements Tracer.
func (NoopTracer) SetServiceInfo(_, _, _ string) {}

// Extract implements Tracer.
func (NoopTracer) Extract(_ interface{}) (SpanContext, error) {
	return NoopSpanContext{}, nil
}

// Inject implements Tracer.
func (NoopTracer) Inject(_ SpanContext, _ interface{}) error { return nil }

// Stop implements Tracer.
func (NoopTracer) Stop() {}

// TODO(kjn v2): These should be removed. They are here temporarily to facilitate
// the shift to the v2 API.
func (NoopTracer) TracerConf() TracerConf {
	return TracerConf{}
}

func (NoopTracer) SubmitStats(*Span)               {}
func (NoopTracer) SubmitAbandonedSpan(*Span, bool) {}
func (NoopTracer) SubmitChunk(any)                 {}
func (NoopTracer) Flush()                          {}

var _ ddtrace.SpanContext = (*NoopSpanContext)(nil)

// NoopSpanContext is an implementation of ddtrace.SpanContext that is a no-op.
type NoopSpanContext struct{}

// SpanID implements ddtrace.SpanContext.
func (NoopSpanContext) SpanID() uint64 { return 0 }

// TraceID implements ddtrace.SpanContext.
func (NoopSpanContext) TraceID() uint64 { return 0 }

// ForeachBaggageItem implements ddtrace.SpanContext.
func (NoopSpanContext) ForeachBaggageItem(_ func(k, v string) bool) {}

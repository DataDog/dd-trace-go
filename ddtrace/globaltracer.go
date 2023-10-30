// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package ddtrace

import (
	"context"
	"sync/atomic"
)

var (
	// globalTracer stores the current tracer as *Tracer (pointer to interface). The
	// atomic.Value type requires types to be consistent, which requires using *Tracer.
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
func (NoopTracer) StartSpan(_ string, _ ...StartSpanOption) *Span {
	return nil
}

// StartSpanFromContext implements Tracer.
func (NoopTracer) StartSpanFromContext(ctx context.Context, _ string, _ ...StartSpanOption) (*Span, context.Context) {
	return nil, ctx
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

func (NoopTracer) CanComputeStats() bool { return false }

func (NoopTracer) PushChunk(_ *Chunk) {}

var _ SpanContext = (*NoopSpanContext)(nil)

// NoopSpanContext is an implementation of SpanContext that is a no-op.
type NoopSpanContext struct{}

// SpanID implements SpanContext.
func (NoopSpanContext) SpanID() uint64 { return 0 }

// TraceID implements SpanContext.
func (NoopSpanContext) TraceID() uint64 { return 0 }

// ForeachBaggageItem implements SpanContext.
func (NoopSpanContext) ForeachBaggageItem(_ func(k, v string) bool) {}

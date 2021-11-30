// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal // import "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"

import (
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

var (
	mu           sync.RWMutex   // guards globalTracer
	globalTracer ddtrace.Tracer = &NoopTracer{}
)

// SetGlobalTracer sets the global tracer to t.
func SetGlobalTracer(t ddtrace.Tracer) {
	mu.Lock()
	old := globalTracer
	globalTracer = t
	// Unlock before potentially calling Stop, to allow any shutdown mechanism
	// to retrieve the active tracer without causing a deadlock on mutex mu.
	mu.Unlock()
	if !Testing {
		// avoid infinite loop when calling (*mocktracer.Tracer).Stop
		old.Stop()
	}
}

// GetGlobalTracer returns the currently active tracer.
func GetGlobalTracer() ddtrace.Tracer {
	mu.RLock()
	defer mu.RUnlock()
	return globalTracer
}

// Testing is set to true when the mock tracer is active. It usually signifies that we are in a test
// environment. This value is used by tracer.Start to prevent overriding the GlobalTracer in tests.
var Testing = false

var _ ddtrace.Tracer = (*NoopTracer)(nil)

// NoopTracer is an implementation of ddtrace.Tracer that is a no-op.
type NoopTracer struct{}

// StartSpan implements ddtrace.Tracer.
func (NoopTracer) StartSpan(operationName string, opts ...ddtrace.StartSpanOption) ddtrace.Span {
	log.Info("NoopTracer starts a span")
	return NoopSpan{}
}

// SetServiceInfo implements ddtrace.Tracer.
func (NoopTracer) SetServiceInfo(_, _, _ string) {
	log.Info("NoopTracer sets a service info, so nothing happens")
}

// Extract implements ddtrace.Tracer.
func (NoopTracer) Extract(_ interface{}) (ddtrace.SpanContext, error) {
	log.Info("NoopTracer extracts a span context, so nothing happens")
	return NoopSpanContext{}, nil
}

// Inject implements ddtrace.Tracer.
func (NoopTracer) Inject(_ ddtrace.SpanContext, _ interface{}) error {
	log.Info("NoopTracer injects a span context, so nothing happens")
	return nil
}

// Stop implements ddtrace.Tracer.
func (NoopTracer) Stop() {
	log.Info("NoopTracer stops tracing, so nothing happens")
}

var _ ddtrace.Span = (*NoopSpan)(nil)

// NoopSpan is an implementation of ddtrace.Span that is a no-op.
type NoopSpan struct{}

// SetTag implements ddtrace.Span.
func (NoopSpan) SetTag(_ string, _ interface{}) {
	log.Info("NoopSpan sets a key-value set, so nothing happens")
}

// SetOperationName implements ddtrace.Span.
func (NoopSpan) SetOperationName(_ string) {
	log.Info("NoopSpan sets a operation name, so nothing happens")
}

// BaggageItem implements ddtrace.Span.
func (NoopSpan) BaggageItem(_ string) string {
	log.Info("NoopSpan returns a baggage item, so nothing happens")
	return ""
}

// SetBaggageItem implements ddtrace.Span.
func (NoopSpan) SetBaggageItem(_, _ string) {
	log.Info("NoopSpan set a baggage item, so nothing happens")
}

// Finish implements ddtrace.Span.
func (NoopSpan) Finish(_ ...ddtrace.FinishOption) {
	log.Info("NoopSpan finishes")
}

// Tracer implements ddtrace.Span.
func (NoopSpan) Tracer() ddtrace.Tracer {
	log.Info("NoopSpan returns a NoopTracer")
	return NoopTracer{}
}

// Context implements ddtrace.Span.
func (NoopSpan) Context() ddtrace.SpanContext {
	log.Info("NoopSpan returns a NoopSpanContext")
	return NoopSpanContext{}
}

var _ ddtrace.SpanContext = (*NoopSpanContext)(nil)

// NoopSpanContext is an implementation of ddtrace.SpanContext that is a no-op.
type NoopSpanContext struct{}

// SpanID implements ddtrace.SpanContext.
func (NoopSpanContext) SpanID() uint64 { return 0 }

// TraceID implements ddtrace.SpanContext.
func (NoopSpanContext) TraceID() uint64 { return 0 }

// ForeachBaggageItem implements ddtrace.SpanContext.
func (NoopSpanContext) ForeachBaggageItem(handler func(k, v string) bool) {}

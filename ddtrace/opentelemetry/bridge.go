// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package opentelemetry

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	_ "unsafe" // for go:linkname

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/internal"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// The functions in this file link the OpenTelemetry trace API to dd-trace-go when the
// application is built with Orchestrion. The aspects in orchestrion.yml use go:linkname to
// call them, because go.opentelemetry.io/otel cannot import this package: this package
// imports go.opentelemetry.io/otel/trace.

var (
	bridgeOnce       sync.Once
	bridgeEnabled    bool
	bridgeTracerInst *oteltracer

	// bridgeReady is true after the initialization of this package. Code that Orchestrion weaves
	// into go.opentelemetry.io/otel calls the bridge, but that code does not import this package. So
	// it can run during package initialization, before the packages of the tracer are ready. The
	// bridge does nothing until then.
	bridgeReady atomic.Bool
)

func init() { bridgeReady.Store(true) }

func loadBridge() {
	bridgeOnce.Do(func() {
		cfg := internalconfig.Get()
		bridgeEnabled = cfg.TraceOTelEnabled()
		p := &TracerProvider{bridge: true}
		p.tracer = &oteltracer{provider: p, otelSemanticsEnabled: cfg.OTelSemanticsEnabled()}
		bridgeTracerInst = p.tracer
	})
}

// bridgeTracer returns the tracer that the OpenTelemetry global tracer uses while the
// application has not set a TracerProvider. It does not start the Datadog tracer. Each span
// goes to the global Datadog tracer that is active when the span starts.
// It returns nil when DD_TRACE_OTEL_ENABLED is not true, and while the global Datadog tracer is
// a no-op tracer. The OpenTelemetry global tracer then returns its usual non-recording span.
//
//go:linkname bridgeTracer
func bridgeTracer() oteltrace.Tracer {
	if !bridgeReady.Load() {
		return nil
	}
	loadBridge()
	if !bridgeEnabled || datadogTracerInactive() {
		return nil
	}
	return bridgeTracerInst
}

// bridgeActive reports whether the bridge links the OpenTelemetry API to the tracer. The woven
// trace.SpanFromContext calls it first, so that it reads the OpenTelemetry span key only when
// the bridge is on.
//
//go:linkname bridgeActive
func bridgeActive() bool {
	if !bridgeReady.Load() {
		return false
	}
	loadBridge()
	return bridgeEnabled
}

// datadogTracerInactive reports whether the global Datadog tracer is a no-op tracer.
func datadogTracerInactive() bool {
	switch internal.GetGlobalTracer[tracer.Tracer]().(type) {
	case tracer.NoopTracer, *tracer.NoopTracer:
		return true
	}
	return false
}

// bridgeOTelTracer returns the tracer that borrowed views use. It loads the bridge once. It does
// not check DD_TRACE_OTEL_ENABLED: callers check bridgeEnabled first.
func bridgeOTelTracer() *oteltracer {
	loadBridge()
	return bridgeTracerInst
}

//go:linkname spanFinished github.com/DataDog/dd-trace-go/v2/ddtrace/tracer.spanFinished
func spanFinished(s *tracer.Span) bool

//go:linkname borrowSpanFromContext github.com/DataDog/dd-trace-go/v2/ddtrace/tracer.borrowSpanFromContext
func borrowSpanFromContext(ctx context.Context, exclude *tracer.Span) (*tracer.Span, *tracer.SpanContext)

// bridgeSpanFromContext returns the span that trace.SpanFromContext must return when ctx
// carries a Datadog span that the OpenTelemetry span key does not show. current is the
// value of the OpenTelemetry span key in ctx. The function returns false when
// trace.SpanFromContext must keep its own result.
//
// It reads only the span that tracer.ContextWithSpan put in ctx, not a span from another
// source such as the Orchestrion goroutine-local stack. It pins the span, so the span pool
// does not reuse the span while a borrowed view of it exists.
//
// It must not call trace.SpanFromContext, because that function calls it.
//
//go:linkname bridgeSpanFromContext
func bridgeSpanFromContext(ctx context.Context, current any) (oteltrace.Span, bool) {
	if !bridgeReady.Load() {
		return nil, false
	}
	loadBridge()
	if !bridgeEnabled {
		return nil, false
	}
	var exclude *tracer.Span
	switch s := current.(type) {
	case nil:
		// No OpenTelemetry span in ctx. Show the Datadog span.
	case *span:
		// Skip the Datadog span of this OpenTelemetry span while it records. After End, the span pool
		// can reuse its Datadog span for a new span, so the exclusion stops.
		s.mu.RLock()
		ended := s.finished
		s.mu.RUnlock()
		if !ended {
			exclude = s.DD
		}
	case *borrowedSpan:
		exclude = s.ddSpan
	default:
		// A span from another OpenTelemetry implementation, or a remote span context.
		// Keep it: the order of the two spans is not known. Spans that the global tracer then
		// starts take the Datadog span in ctx as parent, not this span.
		return nil, false
	}
	ddSpan, _ := borrowSpanFromContext(ctx, exclude)
	if ddSpan == nil {
		return nil, false
	}
	return &borrowedSpan{ddSpan: ddSpan, tracer: bridgeOTelTracer()}, true
}

// borrowedSpan is the OpenTelemetry view of a span that a Datadog integration started and
// owns. Unlike span, it does not keep data until End: each call writes to the Datadog span
// at once, because the integration finishes the span with tracer.Span.Finish.
type borrowedSpan struct {
	noop.Span // https://pkg.go.dev/go.opentelemetry.io/otel/trace#hdr-API_Implementations
	ddSpan    *tracer.Span
	tracer    *oteltracer
}

var _ oteltrace.Span = (*borrowedSpan)(nil)

// live reports whether the view still describes the span that it was created for. It is false
// after the integration that owns the span finishes it. Every method that changes the span
// returns at once when live is false, because the integration owns the span.
func (s *borrowedSpan) live() bool {
	return !spanFinished(s.ddSpan)
}

// SpanContext returns the context of the Datadog span. The span is local: a Datadog
// integration in this process started it. A finished span keeps its span context.
// Remote is false, because the span is local. Spans that this package starts report Remote
// true, which is a known difference.
func (s *borrowedSpan) SpanContext() oteltrace.SpanContext {
	return ddSpanContext(s.ddSpan).WithRemote(false)
}

// IsRecording returns false after the integration that owns the span finished it.
func (s *borrowedSpan) IsRecording() bool { return s.live() }

func (s *borrowedSpan) SetAttributes(kv ...attribute.KeyValue) {
	if !s.live() {
		return
	}
	for _, kv := range kv {
		if k, v := toReservedAttributes(string(kv.Key), kv.Value, s.tracer.otelSemanticsEnabled); k != "" {
			s.ddSpan.SetTag(k, v)
		}
	}
}

// SetStatus sets the error state of the Datadog span. otelcodes.Ok clears it, as Ok has the
// highest precedence in OpenTelemetry. borrowedSpan keeps no status state: a later
// otelcodes.Error sets the error again.
func (s *borrowedSpan) SetStatus(code otelcodes.Code, description string) {
	if !s.live() {
		return
	}
	switch code {
	case otelcodes.Error:
		if description != "" {
			s.ddSpan.SetTag(ext.ErrorMsg, description)
		}
		s.ddSpan.SetTag(ext.Error, true)
	case otelcodes.Ok:
		s.ddSpan.SetTag(ext.Error, false)
	}
}

func (s *borrowedSpan) AddEvent(name string, opts ...oteltrace.EventOption) {
	if !s.live() {
		return
	}
	e := newSpanEvent(name, opts...)
	s.ddSpan.AddEvent(e.name, e.options...)
}

func (s *borrowedSpan) RecordError(err error, opts ...oteltrace.EventOption) {
	if err == nil || !s.live() {
		return
	}
	s.AddEvent("exception", exceptionEventOptions(err, opts)...)
}

func (s *borrowedSpan) SetName(name string) {
	if !s.live() {
		return
	}
	if s.tracer.otelSemanticsEnabled {
		s.ddSpan.SetTag(ext.ResourceName, name)
		return
	}
	s.ddSpan.SetTag(ext.SpanName, strings.ToLower(name))
}

// End finishes the Datadog span, as the OpenTelemetry API specifies.
func (s *borrowedSpan) End(options ...oteltrace.SpanEndOption) {
	if !s.live() {
		return
	}
	var opts []tracer.FinishOption
	cfg := oteltrace.NewSpanEndConfig(options...)
	if t := cfg.Timestamp(); !t.IsZero() {
		opts = append(opts, tracer.FinishTime(t))
	}
	s.ddSpan.Finish(opts...)
}

// TracerProvider returns the global TracerProvider. The application can set its own
// TracerProvider. Instrumentation such as otelhttp starts child spans with the provider of
// the span in the context.
func (s *borrowedSpan) TracerProvider() oteltrace.TracerProvider {
	return otel.GetTracerProvider()
}

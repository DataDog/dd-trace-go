// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

// Package opentelemetry provides a wrapper on top of the Datadog tracer that can be used with OpenTelemetry.
// This feature is currently in beta.
// It also provides a wrapper around TracerProvider to propagate a list of tracer.StartOption
// that are specific to Datadog's APM product. To use it, simply call "NewTracerProvider".
//
//	provider := opentelemetry.NewTracerProvider(tracer.WithService("opentelemetry_service"))
//
// When using Datadog, the OpenTelemetry span name is what is called operation name in Datadog's terms.
// Below is an example setting the tracer provider, initializing a tracer, and creating a span.
//
//	otel.SetTracerProvider(opentelemetry.NewTracerProvider())
//	tracer := otel.Tracer("")
//	ctx, sp := tracer.Start(context.Background(), "span_name")
//	yourCode(ctx)
//	sp.End()
//
// Not every feature provided by OpenTelemetry is supported with this wrapper today, and any new API methods
// added to the OpenTelemetry API will default to being a no-op until implemented by this library. See the
// OpenTelemetry package docs for more details: https://pkg.go.dev/go.opentelemetry.io/otel/trace#hdr-API_Implementations.
// This package seeks to implement a minimal set of functions within
// the OpenTelemetry Tracing API (https://opentelemetry.io/docs/reference/specification/trace/api)
// to allow users to send traces to Datadog using existing OpenTelemetry code with minimal changes to the application.
// Span events (https://opentelemetry.io/docs/concepts/signals/traces/#span-events) are not supported at this time.
package opentelemetry

import (
	"sync"
	"sync/atomic"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	oteltrace "go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

var _ oteltrace.TracerProvider = (*TracerProvider)(nil)

// TracerProvider provides implementation of OpenTelemetry TracerProvider interface.
// TracerProvider provides Tracers that are used by instrumentation code to
// trace computational workflows.
// WithInstrumentationVersion and WithSchemaURL TracerOptions are not supported.
type TracerProvider struct {
	noop.TracerProvider // https://pkg.go.dev/go.opentelemetry.io/otel/trace#hdr-API_Implementations
	tracer              *oteltracer
	stopped             uint32 // stopped indicates whether the tracerProvider has been shutdown.
	sync.Once
}

// NewTracerProvider returns an instance of an OpenTelemetry TracerProvider,
// and initializes the Datadog Tracer with the provided start options.
// This TracerProvider only supports a singleton tracer, and repeated calls to
// the Tracer() method will return the same instance each time.
func NewTracerProvider(opts ...tracer.StartOption) *TracerProvider {
	tracer.Start(opts...)
	p := &TracerProvider{}
	t := &oteltracer{
		DD:       internal.GetGlobalTracer(),
		provider: p,
	}
	p.tracer = t
	return p
}

// Tracer returns the singleton tracer created when NewTracerProvider was called, ignoring
// the provided name and any provided options to this method.
// If the TracerProvider has already been shut down, this will return a no-op tracer.
func (p *TracerProvider) Tracer(_ string, _ ...oteltrace.TracerOption) oteltrace.Tracer {
	if atomic.LoadUint32(&p.stopped) != 0 {
		return noop.NewTracerProvider().Tracer("")
	}
	return p.tracer
}

// Shutdown stops the started tracer. Subsequent calls are valid but become no-op.
func (p *TracerProvider) Shutdown() error {
	p.Once.Do(func() {
		tracer.Stop()
		atomic.StoreUint32(&p.stopped, 1)
	})
	return nil
}

// ForceFlush flushes any buffered traces. Flush is in effect only if a tracer
// is started.
func (p *TracerProvider) ForceFlush(timeout time.Duration, callback func(ok bool)) {
	p.forceFlush(timeout, callback, tracer.Flush)
}

func (p *TracerProvider) forceFlush(timeout time.Duration, callback func(ok bool), flush func()) {
	if atomic.LoadUint32(&p.stopped) != 0 {
		log.Warn("Cannot perform (*TracerProvider).Flush since the tracer is already stopped.")
		return
	}
	done := make(chan struct{})
	go func() {
		flush()
		done <- struct{}{}
	}()
	for {
		select {
		case <-time.After(timeout):
			callback(false)
			return
		case <-done:
			callback(true)
			return
		}
	}
}

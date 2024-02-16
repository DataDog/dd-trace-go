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
	"time"

	v2 "github.com/DataDog/dd-trace-go/v2/ddtrace/opentelemetry"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

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
	v2tracerProvider    *v2.TracerProvider
}

// NewTracerProvider returns an instance of an OpenTelemetry TracerProvider,
// and initializes the Datadog Tracer with the provided start options.
// This TracerProvider only supports a singleton tracer, and repeated calls to
// the Tracer() method will return the same instance each time.
func NewTracerProvider(opts ...tracer.StartOption) *TracerProvider {
	tp := v2.NewTracerProvider(opts...)
	p := &TracerProvider{
		v2tracerProvider: tp,
	}
	return p
}

// Tracer returns the singleton tracer created when NewTracerProvider was called, ignoring
// the provided name and any provided options to this method.
// If the TracerProvider has already been shut down, this will return a no-op tracer.
func (p *TracerProvider) Tracer(_ string, _ ...oteltrace.TracerOption) oteltrace.Tracer {
	return p.v2tracerProvider.Tracer("")
}

// Shutdown stops the started tracer. Subsequent calls are valid but become no-op.
func (p *TracerProvider) Shutdown() error {
	_ = p.v2tracerProvider.Shutdown()
	return nil
}

// ForceFlush flushes any buffered traces. Flush is in effect only if a tracer
// is started.
func (p *TracerProvider) ForceFlush(timeout time.Duration, callback func(ok bool)) {
	p.v2tracerProvider.ForceFlush(timeout, callback)
}

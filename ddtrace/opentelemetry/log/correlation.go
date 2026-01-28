// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
	"context"
	"encoding/binary"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	oteltrace "go.opentelemetry.io/otel/trace"
)

// ddSpanWrapper wraps a Datadog span to implement the OTel Span interface minimally.
// This allows DD spans to be visible to OTel APIs like trace.SpanFromContext.
type ddSpanWrapper struct {
	oteltrace.Span // Embed noop span for unimplemented methods
	dd             *tracer.Span
	spanContext    oteltrace.SpanContext
}

// SpanContext returns the OTel SpanContext derived from the DD span.
func (w *ddSpanWrapper) SpanContext() oteltrace.SpanContext {
	return w.spanContext
}

// IsRecording returns true if the span is recording.
func (w *ddSpanWrapper) IsRecording() bool {
	// This always returns true because DD spans don't expose a "finished" state
	// through the public API. In practice, this is acceptable because logs are
	// typically emitted while spans are active (before Finish is called).
	return true
}

// ddSpanContextToOtel converts a Datadog span context to an OTel SpanContext.
func ddSpanContextToOtel(ddCtx *tracer.SpanContext) oteltrace.SpanContext {
	// Convert DD trace ID (128-bit) to OTel TraceID
	var traceID oteltrace.TraceID
	traceID = ddCtx.TraceIDBytes()

	// Convert DD span ID (64-bit) to OTel SpanID (64-bit)
	var spanID oteltrace.SpanID
	binary.BigEndian.PutUint64(spanID[:], ddCtx.SpanID())

	// Create OTel span context
	config := oteltrace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: oteltrace.FlagsSampled, // DD spans are sampled by default
		Remote:     false,
	}

	return oteltrace.NewSpanContext(config)
}

// contextWithDDSpan wraps a Datadog span in an OpenTelemetry span context and adds it to the context.
// This allows OpenTelemetry APIs like trace.SpanFromContext to find the Datadog span.
//
// If the context already has an OpenTelemetry span, it is preserved.
// If there's a Datadog span in the context but no OpenTelemetry span, this creates a bridge.
//
// This function is internal and used automatically by ddAwareLogger.
func contextWithDDSpan(ctx context.Context) context.Context {
	// Check if there's already an OTel span in the context
	if oteltrace.SpanFromContext(ctx).SpanContext().IsValid() {
		// OTel span already present, no need to bridge
		return ctx
	}

	// Check if there's a DD span in the context
	ddSpan, ok := tracer.SpanFromContext(ctx)
	if !ok {
		// No DD span, return original context
		return ctx
	}

	// Create an OTel span wrapper for the DD span
	otelSpanCtx := ddSpanContextToOtel(ddSpan.Context())

	// Verify the span context is valid
	if !otelSpanCtx.IsValid() {
		// Something went wrong with conversion, return original context
		return ctx
	}

	wrapper := &ddSpanWrapper{
		Span:        oteltrace.SpanFromContext(ctx), // Use existing (noop) span
		dd:          ddSpan,
		spanContext: otelSpanCtx,
	}

	// Add the wrapped span to the context
	return oteltrace.ContextWithSpan(ctx, wrapper)
}

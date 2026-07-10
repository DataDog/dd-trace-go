// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package opentelemetry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// mapCarrier is a simple propagation.TextMapCarrier backed by a string map.
type mapCarrier map[string]string

func (m mapCarrier) Get(key string) string { return m[key] }
func (m mapCarrier) Set(key, val string)   { m[key] = val }
func (m mapCarrier) Keys() []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func TestDatadogPropagatorFields(t *testing.T) {
	fields := DatadogPropagator{}.Fields()
	assert.Contains(t, fields, tracer.DefaultTraceIDHeader)
	assert.Contains(t, fields, tracer.DefaultParentIDHeader)
	assert.Contains(t, fields, tracer.DefaultPriorityHeader)
}

func TestDatadogPropagatorInject(t *testing.T) {
	tp, _, cleanup := mockTracerProvider(t)
	defer cleanup()
	otel.SetTracerProvider(tp)

	tr := tp.Tracer("")
	ctx, span := tr.Start(context.Background(), "root")
	defer span.End()

	carrier := make(mapCarrier)
	DatadogPropagator{}.Inject(ctx, carrier)

	assert.NotEmpty(t, carrier[tracer.DefaultTraceIDHeader], "trace-id header should be set")
	assert.NotEmpty(t, carrier[tracer.DefaultParentIDHeader], "parent-id header should be set")
}

func TestDatadogPropagatorInjectNoSpan(t *testing.T) {
	_, _, cleanup := mockTracerProvider(t)
	defer cleanup()

	carrier := make(mapCarrier)
	DatadogPropagator{}.Inject(context.Background(), carrier)
	assert.Empty(t, carrier, "no headers should be written when context has no span")
}

func TestDatadogPropagatorExtractEmpty(t *testing.T) {
	_, _, cleanup := mockTracerProvider(t)
	defer cleanup()

	ctx := DatadogPropagator{}.Extract(context.Background(), make(mapCarrier))
	_, ok := spanOptionsFromContext(ctx)
	assert.False(t, ok, "no start options should be stored when headers are absent")
}

func TestDatadogPropagatorExtractContinue(t *testing.T) {
	tp, _, cleanup := mockTracerProvider(t)
	defer cleanup()
	otel.SetTracerProvider(tp)

	carrier := mapCarrier{
		tracer.DefaultTraceIDHeader:  "1",
		tracer.DefaultParentIDHeader: "1",
		tracer.DefaultPriorityHeader: "1",
	}

	ctx := DatadogPropagator{}.Extract(context.Background(), carrier)

	opts, ok := spanOptionsFromContext(ctx)
	require.True(t, ok, "ContextWithStartOptions should have been called")
	// Only ChildOf — no span links for continue behavior.
	assert.Len(t, opts, 1)

	// Starting a span with these opts should produce a child of trace ID 1.
	tr := tp.Tracer("")
	ctx, span := tr.Start(ctx, "child")
	defer span.End()

	ddSpan, ok := tracer.SpanFromContext(ctx)
	require.True(t, ok)
	assert.Equal(t, uint64(1), ddSpan.Context().TraceIDLower(), "should continue the incoming trace")
}

func TestDatadogPropagatorExtractRestart(t *testing.T) {
	// Set env var before starting the tracer so the propagator config picks it up.
	t.Setenv("DD_TRACE_PROPAGATION_BEHAVIOR_EXTRACT", "restart")

	tp, _, cleanup := mockTracerProvider(t)
	defer cleanup()
	otel.SetTracerProvider(tp)

	carrier := mapCarrier{
		tracer.DefaultTraceIDHeader:  "1",
		tracer.DefaultParentIDHeader: "1",
		tracer.DefaultPriorityHeader: "2",
	}

	ctx := DatadogPropagator{}.Extract(context.Background(), carrier)

	opts, ok := spanOptionsFromContext(ctx)
	require.True(t, ok, "ContextWithStartOptions should have been called")
	// Restart: ChildOf + WithSpanLinks.
	assert.Len(t, opts, 2, "restart should produce both ChildOf and WithSpanLinks options")

	// Start a span via the OTel bridge — it picks up the options and creates
	// the span as a restart root with a new trace ID.
	tr := tp.Tracer("")
	ctx, span := tr.Start(ctx, "otel_extract_distant_call")
	defer span.End()

	ddSpan, ok := tracer.SpanFromContext(ctx)
	require.True(t, ok)
	assert.NotEqual(t, uint64(1), ddSpan.Context().TraceIDLower(), "restart should produce a new trace ID")
}

func TestDatadogPropagatorRoundTrip(t *testing.T) {
	tp, _, cleanup := mockTracerProvider(t)
	defer cleanup()
	otel.SetTracerProvider(tp)

	prop := propagation.NewCompositeTextMapPropagator(DatadogPropagator{}, propagation.TraceContext{}, propagation.Baggage{})
	otel.SetTextMapPropagator(prop)

	tr := tp.Tracer("")

	// Start a root span and inject its context into a carrier.
	ctx, root := tr.Start(context.Background(), "root")
	defer root.End()

	carrier := make(mapCarrier)
	prop.Inject(ctx, carrier)
	assert.NotEmpty(t, carrier[tracer.DefaultTraceIDHeader], "inject should write trace-id")

	ddRoot, ok := tracer.SpanFromContext(ctx)
	require.True(t, ok)

	// Extract from the carrier and start a child span.
	childCtx := prop.Extract(context.Background(), carrier)
	childCtx, child := tr.Start(childCtx, "child")
	defer child.End()

	ddChild, ok := tracer.SpanFromContext(childCtx)
	require.True(t, ok)

	// The child span should be in the same trace as the root.
	assert.Equal(t, ddRoot.Context().TraceIDLower(), ddChild.Context().TraceIDLower(), "round-trip should preserve trace ID")
}

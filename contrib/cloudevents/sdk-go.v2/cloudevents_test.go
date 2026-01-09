// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package sdkgov2_test

import (
	"testing"

	sdkgov2 "github.com/DataDog/dd-trace-go/contrib/cloudevents/sdk-go.v2/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/cloudevents/sdk-go/v2/event"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTracingCloudEvents(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	e := event.New()
	e.SetID("1234")
	e.SetType("com.example.test")
	e.SetSource("/test/source")

	ctx := t.Context()
	span, _ := tracer.StartSpanFromContext(ctx, "test.span")
	defer span.Finish()

	carrier := sdkgov2.NewEventCarrier(&e)
	// Important here how we can use the event to inject the context.
	err := tracer.Inject(span.Context(), carrier)
	require.NoError(t, err, "Failed to inject trace context")

	// Now we can actually verify everything propagates properly.
	ext := e.Extensions()
	assert.Contains(t, ext, "traceparent", "Should contain W3C traceparent")
	assert.Contains(t, ext, "tracestate", "Should contain W3C tracestate")

	extractedCtx, err := tracer.Extract(carrier)
	require.NoError(t, err, "Failed to extract trace context")
	assert.NotNil(t, extractedCtx, "Should extract span context")

	t.Logf("Original Trace ID: %s", span.Context().TraceID())
	t.Logf("Extracted Trace ID: %s", extractedCtx.TraceID())
	assert.Equal(t, span.Context().TraceID(), extractedCtx.TraceID(), "Trace IDs should match")
}

// TestExtractTraceContext_NoContext tests extraction when no trace context exists
func TestExtractTraceContext_NoContext(t *testing.T) {
	e := event.New()
	e.SetID("1234")
	e.SetType("com.example.test")
	e.SetSource("/test/source")

	carrier := sdkgov2.NewEventCarrier(&e)
	extractedCtx, err := tracer.Extract(carrier)
	require.NoError(t, err, "Should not error when no trace context exists")
	assert.Nil(t, extractedCtx, "Should return nil when no trace context exists")
}

// TestInjectAndExtractTraceContext_RoundTrip tests the full round trip
func TestInjectAndExtractTraceContext_RoundTrip(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	// Create a span
	span, _ := tracer.StartSpanFromContext(t.Context(), "parent.span")
	// Finish the span (Because this is just a simulation)
	span.Finish()

	// Create event and inject trace
	e := event.New()
	e.SetID("test-id")
	e.SetType("test.type")
	e.SetSource("/test")

	carrier := sdkgov2.NewEventCarrier(&e)
	err := tracer.Inject(span.Context(), carrier)
	require.NoError(t, err)

	// Extract and verify
	extractedCtx, err := tracer.Extract(carrier)
	require.NoError(t, err)
	require.NotNil(t, extractedCtx)

	childSpan := span.StartChild("child.span")
	childSpan.Finish()

	// Verify the spans are linked
	spans := mt.FinishedSpans()
	require.Len(t, spans, 2, "Should have parent and child spans")
	assert.Equal(t, spans[0].TraceID(), spans[1].TraceID(), "Child should have same trace ID as parent")
}

// TestEventCarrier_Set tests the Set method of EventCarrier
func TestEventCarrier_Set(t *testing.T) {
	e := event.New()
	e.SetID("test-id")

	carrier := sdkgov2.NewEventCarrier(&e)

	// Test setting valid W3C headers
	carrier.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	carrier.Set("tracestate", "dd=s:1")

	ext := e.Extensions()
	assert.Equal(t, "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", ext["traceparent"])
	assert.Equal(t, "dd=s:1", ext["tracestate"])

	// Test that invalid extension names are filtered out (names with hyphens)
	carrier.Set("x-datadog-trace-id", "12345")
	assert.NotContains(t, ext, "x-datadog-trace-id", "Should not set invalid extension names")
}

// TestEventCarrier_ForeachKey tests the ForeachKey method of EventCarrier
func TestEventCarrier_ForeachKey(t *testing.T) {
	e := event.New()
	e.SetID("test-id")
	e.SetExtension("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	e.SetExtension("tracestate", "dd=s:1")
	e.SetExtension("customext", "value")

	carrier := sdkgov2.NewEventCarrier(&e)

	collected := make(map[string]string)
	err := carrier.ForeachKey(func(key, val string) error {
		collected[key] = val
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", collected["traceparent"])
	assert.Equal(t, "dd=s:1", collected["tracestate"])
	assert.Equal(t, "value", collected["customext"])
}

// TestEventCarrier_ForeachKey_NoExtensions tests ForeachKey with no extensions
func TestEventCarrier_ForeachKey_NoExtensions(t *testing.T) {
	e := event.New()
	e.SetID("test-id")

	carrier := sdkgov2.NewEventCarrier(&e)

	called := false
	err := carrier.ForeachKey(func(key, val string) error {
		called = true
		return nil
	})

	require.NoError(t, err)
	assert.False(t, called, "Should not call handler when no extensions exist")
}

// TestEventCarrier_RoundTrip tests inject and extract using the carrier
func TestEventCarrier_RoundTrip(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	// Create a span
	span, _ := tracer.StartSpanFromContext(t.Context(), "parent.span")
	defer span.Finish()

	// Create event and inject using carrier
	e := event.New()
	e.SetID("test-id")
	e.SetType("test.type")
	e.SetSource("/test")

	carrier := sdkgov2.NewEventCarrier(&e)
	err := tracer.Inject(span.Context(), carrier)
	require.NoError(t, err)

	// Extract using carrier and verify
	extractedCarrier := sdkgov2.NewEventCarrier(&e)
	extractedCtx, err := tracer.Extract(extractedCarrier)
	require.NoError(t, err)
	require.NotNil(t, extractedCtx)

	assert.Equal(t, span.Context().TraceID(), extractedCtx.TraceID(), "Trace IDs should match")
}

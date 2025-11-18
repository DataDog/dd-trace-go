package sdkgov2_test

import (
	"testing"

	sdkgov2 "github.com/DataDog/dd-trace-go/contrib/cloudevents/sdkgo.v2"
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

	// Important here how we can use the event to inject the context.
	err := sdkgov2.InjectTraceContext(span.Context(), &e)
	require.NoError(t, err, "Failed to inject trace context")

	// Now we can actually verify everything propagates properly.
	ext := e.Extensions()
	assert.Contains(t, ext, "traceparent", "Should contain W3C traceparent")
	assert.Contains(t, ext, "tracestate", "Should contain W3C tracestate")

	extractedCtx, err := sdkgov2.ExtractTraceContext(e)
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

	extractedCtx, err := sdkgov2.ExtractTraceContext(e)
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

	err := sdkgov2.InjectTraceContext(span.Context(), &e)
	require.NoError(t, err)

	// Extract and verify
	extractedCtx, err := sdkgov2.ExtractTraceContext(e)
	require.NoError(t, err)
	require.NotNil(t, extractedCtx)

	childSpan := span.StartChild("child.span")
	childSpan.Finish()

	// Verify the spans are linked
	spans := mt.FinishedSpans()
	require.Len(t, spans, 2, "Should have parent and child spans")
	assert.Equal(t, spans[0].TraceID(), spans[1].TraceID(), "Child should have same trace ID as parent")
}

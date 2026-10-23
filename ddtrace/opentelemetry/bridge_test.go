// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package opentelemetry

import (
	"context"
	"errors"
	"testing"
	"time"
	_ "unsafe" // for go:linkname

	"github.com/DataDog/dd-trace-go/v2/ddtrace/internal"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	sharedinternal "github.com/DataDog/dd-trace-go/v2/internal"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// setBridge enables or disables the bridge for one test and restores the lazy state after it.
func setBridge(t *testing.T, enabled bool) {
	t.Helper()
	loadBridge()
	prev := bridgeEnabled
	bridgeEnabled = enabled
	t.Cleanup(func() { bridgeEnabled = prev })
}

//go:linkname spanContextWithOwnerTest github.com/DataDog/dd-trace-go/v2/ddtrace/tracer.spanContextWithOwner
func spanContextWithOwnerTest(sc *tracer.SpanContext, s *tracer.Span) *tracer.SpanContext

// bindOwner makes s borrowable in builds without Orchestrion, where spanStart does not record
// the owner. Call it before ctx is shared.
func bindOwner(s *tracer.Span) { spanContextWithOwnerTest(s.Context(), s) }

// flushedSpans flushes the tracer and returns the spans of the next payload by resource name.
func flushedSpans(t *testing.T, payloads chan traces) map[string][]map[string]any {
	t.Helper()
	tracer.Flush()
	p, err := waitForPayload(payloads)
	require.NoError(t, err)
	spans := make(map[string][]map[string]any)
	for _, chunk := range p {
		for _, s := range chunk {
			resource, _ := s["resource"].(string)
			spans[resource] = append(spans[resource], s)
		}
	}
	return spans
}

// borrowDatadogSpan starts a Datadog span without OpenTelemetry and returns it with its
// borrowed OpenTelemetry view.
func borrowDatadogSpan(t *testing.T) (*tracer.Span, context.Context, oteltrace.Span) {
	t.Helper()
	setBridge(t, true)
	ddSpan, ctx := tracer.StartSpanFromContext(context.Background(), "dd.op")
	bindOwner(ddSpan)
	b, ok := bridgeSpanFromContext(ctx, nil)
	require.True(t, ok)
	require.IsType(t, &borrowedSpan{}, b)
	return ddSpan, ctx, b
}

func TestBridgeSpanFromContextNoDatadogSpan(t *testing.T) {
	setBridge(t, true)
	sp, ok := bridgeSpanFromContext(context.Background(), nil)
	assert.False(t, ok)
	assert.Nil(t, sp)
}

func TestBridgeSpanFromContextDatadogSpan(t *testing.T) {
	setBridge(t, true)
	_, _, cleanup := mockTracerProvider(t)
	defer cleanup()

	ddSpan, ctx := tracer.StartSpanFromContext(context.Background(), "dd.op")
	bindOwner(ddSpan)
	defer ddSpan.Finish()
	sp, ok := bridgeSpanFromContext(ctx, nil)
	require.True(t, ok)
	require.IsType(t, &borrowedSpan{}, sp)
	assert.True(t, sp.IsRecording())
	sc := sp.SpanContext()
	assert.True(t, sc.IsValid())
	want := ddSpanContext(ddSpan)
	assert.Equal(t, want.TraceID(), sc.TraceID())
	assert.Equal(t, want.SpanID(), sc.SpanID())
}

func TestBridgeSpanFromContextInSyncOTelSpan(t *testing.T) {
	setBridge(t, true)
	tp, _, cleanup := mockTracerProvider(t)
	defer cleanup()

	ctx, sp := tp.Tracer("").Start(context.Background(), "otel.op")
	defer sp.End()
	got, ok := bridgeSpanFromContext(ctx, sp)
	assert.False(t, ok)
	assert.Nil(t, got)
}

func TestBridgeSpanFromContextStaleOTelSpan(t *testing.T) {
	setBridge(t, true)
	tp, _, cleanup := mockTracerProvider(t)
	defer cleanup()

	ctx, sp := tp.Tracer("").Start(context.Background(), "otel.op")
	defer sp.End()
	child, ctx2 := tracer.StartSpanFromContext(ctx, "dd.child")
	bindOwner(child)
	defer child.Finish()
	got, ok := bridgeSpanFromContext(ctx2, sp)
	require.True(t, ok)
	assert.Equal(t, ddSpanContext(child).SpanID(), got.SpanContext().SpanID())
}

func TestBridgeSpanFromContextBorrowedInSync(t *testing.T) {
	_, _, cleanup := mockTracerProvider(t)
	defer cleanup()

	ddSpan, ctx, b := borrowDatadogSpan(t)
	defer ddSpan.Finish()
	got, ok := bridgeSpanFromContext(ctx, b)
	assert.False(t, ok)
	assert.Nil(t, got)
}

func TestBridgeSpanFromContextForeignSpan(t *testing.T) {
	setBridge(t, true)
	_, _, cleanup := mockTracerProvider(t)
	defer cleanup()

	ddSpan, ctx := tracer.StartSpanFromContext(context.Background(), "dd.op")
	defer ddSpan.Finish()
	sc := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    oteltrace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:     oteltrace.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
		TraceFlags: oteltrace.FlagsSampled,
		Remote:     true,
	})
	require.True(t, sc.IsValid())
	ctx = oteltrace.ContextWithSpanContext(ctx, sc)
	got, ok := bridgeSpanFromContext(ctx, oteltrace.SpanFromContext(ctx))
	assert.False(t, ok)
	assert.Nil(t, got)
}

func TestBorrowedSpanWritesThrough(t *testing.T) {
	_, payloads, cleanup := mockTracerProvider(t)
	defer cleanup()

	ddSpan, _, b := borrowDatadogSpan(t)
	b.SetAttributes(attribute.String("demo.attribute", "value"), attribute.Int("demo.int", 42))
	b.SetStatus(otelcodes.Error, "boom")
	b.RecordError(errors.New("oops"))
	// The integration finishes the span. The test must not call End on the borrowed span.
	ddSpan.Finish()

	spans := flushedSpans(t, payloads)
	require.Len(t, spans["dd.op"], 1)
	s := spans["dd.op"][0]
	meta, ok := s["meta"].(map[string]any)
	require.True(t, ok)
	metrics, ok := s["metrics"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "value", meta["demo.attribute"])
	assert.Equal(t, 42.0, metrics["demo.int"])
	assert.Equal(t, 1.0, s["error"])
	assert.Equal(t, "boom", meta["error.message"])
	events, _ := meta["events"].(string)
	assert.Contains(t, events, `"name":"exception"`)
	assert.Contains(t, events, "oops")
}

func TestBorrowedSpanSetStatusNonError(t *testing.T) {
	_, payloads, cleanup := mockTracerProvider(t)
	defer cleanup()

	ddSpan, _, b := borrowDatadogSpan(t)
	b.SetStatus(otelcodes.Ok, "fine")
	b.SetStatus(otelcodes.Unset, "")
	ddSpan.Finish()

	spans := flushedSpans(t, payloads)
	require.Len(t, spans["dd.op"], 1)
	s := spans["dd.op"][0]
	meta, ok := s["meta"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, 0.0, s["error"])
	assert.NotContains(t, meta, "error.message")
}

func TestBorrowedSpanEndFinishes(t *testing.T) {
	_, payloads, cleanup := mockTracerProvider(t)
	defer cleanup()

	ddSpan, _, b := borrowDatadogSpan(t)
	b.End()

	spans := flushedSpans(t, payloads)
	assert.Len(t, spans["dd.op"], 1)

	// A second Finish must not send the span again.
	ddSpan.Finish()
	tracer.Flush()
	select {
	case p := <-payloads:
		t.Fatalf("unexpected second payload: %v", p)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestBorrowedSpanSetName(t *testing.T) {
	_, payloads, cleanup := mockTracerProvider(t)
	defer cleanup()

	ddSpan, _, b := borrowDatadogSpan(t)
	b.SetName("New.Name")
	ddSpan.Finish()

	spans := flushedSpans(t, payloads)
	require.Len(t, spans["dd.op"], 1)
	assert.Equal(t, "new.name", spans["dd.op"][0]["name"])
}

func TestBridgeTracerParentsToDatadogSpan(t *testing.T) {
	setBridge(t, true)
	_, payloads, cleanup := mockTracerProvider(t)
	defer cleanup()

	ddSpan, ctx := tracer.StartSpanFromContext(context.Background(), "dd.op")
	_, child := bridgeTracer().Start(ctx, "child")
	child.End()
	ddSpan.Finish()

	spans := flushedSpans(t, payloads)
	require.Len(t, spans["dd.op"], 1)
	require.Len(t, spans["child"], 1)
	parent, kid := spans["dd.op"][0], spans["child"][0]
	assert.Equal(t, parent["span_id"], kid["parent_id"])
	assert.Equal(t, parent["trace_id"], kid["trace_id"])
}

func TestBridgeTracerSingletonDoesNotStartTracer(t *testing.T) {
	// An earlier test can leave a Datadog tracer running. Stop it, because this test needs the
	// no-op global tracer at the start.
	tracer.Stop()
	before := internal.GetGlobalTracer[tracer.Tracer]()
	setBridge(t, true)
	first := bridgeTracer()
	second := bridgeTracer()
	after := internal.GetGlobalTracer[tracer.Tracer]()

	assert.Nil(t, first)
	assert.Nil(t, second)
	assert.Equal(t, before, after)

	_, _, cleanup := mockTracerProvider(t)
	defer cleanup()
	startedFirst := bridgeTracer().(*oteltracer)
	startedSecond := bridgeTracer().(*oteltracer)
	assert.Same(t, startedFirst, startedSecond)
}

func TestBridgeTracerInactiveDatadogTracer(t *testing.T) {
	// An earlier test can leave a Datadog tracer running. Stop it, because this test needs the
	// no-op global tracer at the start.
	tracer.Stop()
	setBridge(t, true)
	assert.Nil(t, bridgeTracer())

	_, _, cleanup := mockTracerProvider(t)
	assert.NotNil(t, bridgeTracer())

	cleanup()
	assert.Nil(t, bridgeTracer())
}

func TestBridgeDisabled(t *testing.T) {
	_, _, cleanup := mockTracerProvider(t)
	defer cleanup()
	setBridge(t, false)
	ddSpan, ctx := tracer.StartSpanFromContext(context.Background(), "dd.op")
	require.NotNil(t, ddSpan)
	defer ddSpan.Finish()

	sp, ok := bridgeSpanFromContext(ctx, nil)
	assert.False(t, ok)
	assert.Nil(t, sp)
	assert.Nil(t, bridgeTracer())
}

func TestBorrowedSpanContextIsLocal(t *testing.T) {
	_, _, cleanup := mockTracerProvider(t)
	defer cleanup()

	ddSpan, _, b := borrowDatadogSpan(t)
	defer ddSpan.Finish()
	sc := b.SpanContext()
	assert.True(t, sc.IsValid())
	assert.False(t, sc.IsRemote())
}

func TestBorrowedSpanStopsRecordingAfterFinish(t *testing.T) {
	_, _, cleanup := mockTracerProvider(t)
	defer cleanup()

	ddSpan, _, b := borrowDatadogSpan(t)
	assert.True(t, b.IsRecording())
	ddSpan.Finish()
	assert.False(t, b.IsRecording())
	assert.NotPanics(t, func() {
		b.SetAttributes(attribute.String("late", "value"))
	})
}

func TestBorrowedSpanLateOperations(t *testing.T) {
	_, payloads, cleanup := mockTracerProvider(t)
	defer cleanup()

	ddSpan, _, b := borrowDatadogSpan(t)
	ddSpan.Finish()

	b.SetAttributes(attribute.String("late.attribute", "late"))
	b.SetStatus(otelcodes.Error, "late error")
	b.AddEvent("late event")
	b.RecordError(errors.New("late error"))
	b.SetName("late.name")
	b.End()

	assert.False(t, b.IsRecording())
	assert.True(t, b.SpanContext().IsValid())

	spans := flushedSpans(t, payloads)
	require.Len(t, spans["dd.op"], 1)
	s := spans["dd.op"][0]
	assert.Equal(t, "dd.op", s["name"])
	assert.Equal(t, 0.0, s["error"])
	meta, ok := s["meta"].(map[string]any)
	require.True(t, ok)
	assert.NotContains(t, meta, "late.attribute")
	assert.NotContains(t, meta, "error.message")
	events, _ := meta["events"].(string)
	assert.NotContains(t, events, "late event")
	assert.NotContains(t, events, "late error")
}

func TestBridgeSpanFromContextRawKeyUnsupported(t *testing.T) {
	_, payloads, cleanup := mockTracerProvider(t)
	defer cleanup()
	setBridge(t, true)

	ddSpan := tracer.StartSpan("dd.op")
	bindOwner(ddSpan)
	// A context that stores the span under the raw active-span key has no context snapshot.
	// The bridge reads only the snapshot that ContextWithSpan stores.
	ctx := context.WithValue(context.Background(), sharedinternal.ActiveSpanKey, ddSpan)
	got, ok := bridgeSpanFromContext(ctx, nil)
	assert.False(t, ok)
	assert.Nil(t, got)

	ddSpan.Finish()
	spans := flushedSpans(t, payloads)
	require.Len(t, spans["dd.op"], 1)
}

func TestBorrowedSpanSetStatusOkClearsError(t *testing.T) {
	_, payloads, cleanup := mockTracerProvider(t)
	defer cleanup()

	ddSpan, _, b := borrowDatadogSpan(t)
	b.SetStatus(otelcodes.Error, "boom")
	b.SetStatus(otelcodes.Ok, "")
	ddSpan.Finish()

	spans := flushedSpans(t, payloads)
	require.Len(t, spans["dd.op"], 1)
	assert.Equal(t, 0.0, spans["dd.op"][0]["error"])
}

func TestBorrowedSpanTracerProviderIsGlobal(t *testing.T) {
	_, _, cleanup := mockTracerProvider(t)
	defer cleanup()

	ddSpan, _, b := borrowDatadogSpan(t)
	defer ddSpan.Finish()
	assert.Equal(t, otel.GetTracerProvider(), b.TracerProvider())
}

func TestBridgeProviderShutdownKeepsTracer(t *testing.T) {
	_, _, cleanup := mockTracerProvider(t)
	defer cleanup()
	setBridge(t, true)

	before := internal.GetGlobalTracer[tracer.Tracer]()
	require.NoError(t, bridgeOTelTracer().provider.Shutdown())
	after := internal.GetGlobalTracer[tracer.Tracer]()
	assert.Same(t, before, after)
}

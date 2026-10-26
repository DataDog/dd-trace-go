// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package otelapi

import (
	"context"
	"testing"

	"github.com/DataDog/orchestrion/runtime/built"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseSpanPool checks that a span that the OpenTelemetry API borrowed does not go back to the
// span pool. The application can keep the borrowed span after the integration finishes the span.
// A late write through it must not reach the span of another operation.
type TestCaseSpanPool struct{}

func (*TestCaseSpanPool) PreBootstrap(_ context.Context, t *testing.T) {
	t.Setenv("DD_TRACE_OTEL_ENABLED", "true")
	t.Setenv("DD_TRACER_EXPERIMENTAL_SPAN_POOL_ENABLED", "true")
}

func (*TestCaseSpanPool) Setup(_ context.Context, t *testing.T) {
	if !built.WithOrchestrion {
		t.Skip("orchestrion weaves the OpenTelemetry API bridge")
	}
}

func (*TestCaseSpanPool) Run(ctx context.Context, t *testing.T) {
	borrowed, ctx := tracer.StartSpanFromContext(ctx, "dd.borrowed")
	view := oteltrace.SpanFromContext(ctx)
	require.True(t, view.IsRecording(), "the bridge returned no Datadog span")
	control, _ := tracer.StartSpanFromContext(ctx, "dd.control")
	control.Finish()
	borrowed.Finish()

	// The flush handler processes the chunk, so the flush waits for the release.
	tracer.Flush()

	// The control span proves that the pool is on and that the release ran for this chunk.
	require.Empty(t, control.AsMap()[ext.SpanName], "the span pool did not release the control span")

	// releaseSpans clears a span before it returns the span to the pool. A pinned span keeps its
	// name, so this check does not depend on which span the pool gives back.
	require.Equal(t, "dd.borrowed", borrowed.AsMap()[ext.SpanName], "the span pool released a borrowed span")

	view.SetAttributes(attribute.String("late.write", "leak"))

	for range 100 {
		s, _ := tracer.StartSpanFromContext(context.Background(), "dd.after")
		require.NotSame(t, borrowed, s, "the span pool reused a borrowed span")
		require.NotContains(t, s.AsMap(), "late.write", "a late write reached the span of another operation")
		s.Finish()
	}
}

func (*TestCaseSpanPool) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "dd.borrowed",
			},
		},
		{
			Tags: map[string]any{
				"name": "dd.after",
			},
		},
	}
}

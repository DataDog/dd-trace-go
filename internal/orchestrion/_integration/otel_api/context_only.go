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
	"go.opentelemetry.io/otel"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseContextOnly checks that the OpenTelemetry API finds the active span only through the context.
// A Datadog span that is on the goroutine-local stack must not become visible to trace.SpanFromContext
// or the parent of an OpenTelemetry span that starts with context.Background.
type TestCaseContextOnly struct{ bridgeEnabled }

func (*TestCaseContextOnly) Setup(_ context.Context, t *testing.T) {
	if !built.WithOrchestrion {
		t.Skip("orchestrion weaves the OpenTelemetry API bridge")
	}
}

func (*TestCaseContextOnly) Run(ctx context.Context, t *testing.T) {
	parent, _ := tracer.StartSpanFromContext(ctx, "dd.parent") // on the GLS under Orchestrion
	require.False(t, oteltrace.SpanFromContext(context.Background()).SpanContext().IsValid(),
		"trace.SpanFromContext must not find the Datadog span on the goroutine")
	_, child := otel.Tracer("otel_api").Start(context.Background(), "otel.background")
	require.NotEqual(t, oteltrace.TraceID(parent.Context().TraceIDBytes()), child.SpanContext().TraceID(),
		"an OpenTelemetry span started with context.Background must start a new trace")
	child.End()
	parent.Finish()
}

func (*TestCaseContextOnly) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "dd.parent",
			},
		},
		{
			Tags: map[string]any{
				"name":     "internal",
				"resource": "otel.background",
			},
		},
	}
}

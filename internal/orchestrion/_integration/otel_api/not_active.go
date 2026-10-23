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

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseNotActive checks that an OpenTelemetry span is not put on the goroutine-local stack.
// A span that stays open must not become the parent of a span that starts without it in its context.
type TestCaseNotActive struct{ bridgeEnabled }

func (*TestCaseNotActive) Setup(_ context.Context, t *testing.T) {
	if !built.WithOrchestrion {
		t.Skip("orchestrion weaves the OpenTelemetry API bridge")
	}
}

func (*TestCaseNotActive) Run(ctx context.Context, t *testing.T) {
	_, open := otel.Tracer("otel_api").Start(ctx, "otel.open")
	_, active := tracer.SpanFromContext(context.Background())
	require.False(t, active, "an OpenTelemetry span must not be active on the goroutine")
	unrelated, _ := tracer.StartSpanFromContext(context.Background(), "dd.unrelated")
	require.Zero(t, unrelated.AsMap()[ext.MapSpanParentID], "the unrelated span must be a root")
	require.NotEqual(t, open.SpanContext().TraceID(), oteltrace.TraceID(unrelated.Context().TraceIDBytes()),
		"the unrelated span must be in another trace")
	unrelated.Finish()
	open.End()
}

func (*TestCaseNotActive) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name":     "internal",
				"resource": "otel.open",
			},
		},
		{
			Tags: map[string]any{
				"name": "dd.unrelated",
			},
		},
	}
}

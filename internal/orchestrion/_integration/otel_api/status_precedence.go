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
	otelcodes "go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseStatusPrecedence checks that an Ok status stays in effect over a later Error status
// on separate borrowed views of one Datadog span.
type TestCaseStatusPrecedence struct{ bridgeEnabled }

func (*TestCaseStatusPrecedence) Setup(_ context.Context, t *testing.T) {
	if !built.WithOrchestrion {
		t.Skip("orchestrion weaves the OpenTelemetry API bridge")
	}
}

func (*TestCaseStatusPrecedence) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "dd.status")
	b1 := oteltrace.SpanFromContext(ctx)
	b1.SetStatus(otelcodes.Ok, "")
	b2 := oteltrace.SpanFromContext(ctx)
	b2.SetStatus(otelcodes.Error, "boom")
	// The mock agent merges no top-level error field into its tag view, so the
	// error state is asserted here instead of in ExpectedTraces.
	asMap := span.AsMap()
	require.Equal(t, int32(0), asMap[ext.MapSpanError], "Ok must win over a later Error")
	require.NotContains(t, asMap, ext.ErrorMsg, "Ok must remove the message of a later Error")
	span.Finish()
}

func (*TestCaseStatusPrecedence) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "dd.status",
			},
		},
	}
}

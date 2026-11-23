// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package otelapi

import (
	"context"
	"testing"

	"github.com/DataDog/orchestrion/runtime/built"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseNested checks that the OpenTelemetry API sees a Datadog span that starts inside an
// OpenTelemetry span.
type TestCaseNested struct{ bridgeEnabled }

func (*TestCaseNested) Setup(_ context.Context, t *testing.T) {
	if !built.WithOrchestrion {
		t.Skip("orchestrion weaves the OpenTelemetry API bridge")
	}
}

func (*TestCaseNested) Run(ctx context.Context, _ *testing.T) {
	ctx, parent := otel.Tracer("otel_api").Start(ctx, "otel.parent")
	annotated(ctx)
	parent.End()
}

func (*TestCaseNested) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name":     "internal",
				"resource": "otel.parent",
			},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name": "dd.annotated",
					},
					Meta: map[string]string{
						"function-name":         "annotated",
						"otel.nested.attribute": "value",
					},
				},
			},
		},
	}
}

//dd:span span.name:dd.annotated
func annotated(ctx context.Context) {
	oteltrace.SpanFromContext(ctx).SetAttributes(attribute.String("otel.nested.attribute", "value"))
}

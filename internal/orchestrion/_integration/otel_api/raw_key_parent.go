// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog, Inc.
// Copyright 2026 Datadog, Inc.

package otelapi

import (
	"context"
	"testing"

	"github.com/DataDog/orchestrion/runtime/built"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseRawKeyParent checks that the OpenTelemetry API takes a Datadog span as
// its parent when the span is only under the raw active-span key. The fasthttp
// integration stores its span that way, as a user value on the request context.
type TestCaseRawKeyParent struct{ bridgeEnabled }

func (*TestCaseRawKeyParent) Setup(_ context.Context, t *testing.T) {
	if !built.WithOrchestrion {
		t.Skip("orchestrion weaves the OpenTelemetry API bridge")
	}
}

func (*TestCaseRawKeyParent) Run(ctx context.Context, t *testing.T) {
	s := tracer.StartSpan("dd.raw")
	rawCtx := context.WithValue(ctx, internal.ActiveSpanKey, s)
	_, child := otel.Tracer("otel_api").Start(rawCtx, "otel.child")
	require.Equal(t, oteltrace.TraceID(s.Context().TraceIDBytes()), child.SpanContext().TraceID(),
		"the child must continue the trace of the span under the raw key")
	child.End()
	s.Finish()
}

func (*TestCaseRawKeyParent) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "dd.raw",
			},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":     "internal",
						"resource": "otel.child",
					},
				},
			},
		},
	}
}

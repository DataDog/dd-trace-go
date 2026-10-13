// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package cloudevents

import (
	"context"
	"testing"

	cloudeventssdk "github.com/cloudevents/sdk-go/v2"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type TestCase struct{}

func (*TestCase) Setup(context.Context, *testing.T) {}

func (*TestCase) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	client, err := newClient()
	require.NoError(t, err)

	event := cloudeventssdk.NewEvent()
	event.SetID("event-id")
	event.SetType("com.example.created")
	event.SetSource("https://example.test/source")
	require.NoError(t, client.Send(ctx, event))
}

func (*TestCase) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{"name": "test.root"},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":     "cloudevents.send",
						"resource": "com.example.created",
						"type":     "queue",
					},
					Meta: map[string]string{
						"component": "cloudevents/sdk-go.v2",
						"span.kind": "producer",
					},
				},
			},
		},
	}
}

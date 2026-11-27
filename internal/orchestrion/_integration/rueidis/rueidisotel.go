// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package rueidis

import (
	"context"
	"testing"

	"github.com/redis/rueidis"
	"github.com/redis/rueidis/rueidisotel"
	"github.com/stretchr/testify/require"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

// TestCaseOtelClient checks that the client rueidisotel creates with
// rueidis.NewClient is not wrapped by otelc, since rueidisotel already traces
// its commands. Its OTel providers are no-ops, so only a wrapped client could
// report a span.
type TestCaseOtelClient struct {
	addr string
}

func (tc *TestCaseOtelClient) PreBootstrap(_ context.Context, t *testing.T) {
	tc.addr = startRedis(t)
}

func (*TestCaseOtelClient) Setup(context.Context, *testing.T) {}

func (tc *TestCaseOtelClient) Run(ctx context.Context, t *testing.T) {
	client, err := rueidisotel.NewClient(clientOption(tc.addr),
		rueidisotel.WithTracerProvider(tracenoop.NewTracerProvider()),
		rueidisotel.WithMeterProvider(metricnoop.NewMeterProvider()),
	)
	require.NoError(t, err)
	defer client.Close()

	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()
	require.True(t, rueidis.IsRedisNil(client.Do(ctx, client.B().Get().Key("rueidisotel").Build()).Error()))
}

// ExpectedSpanCount is test.root alone under otelc. Orchestrion also wraps
// the rueidis.NewClient call inside rueidisotel, so it adds a redis.command
// span.
func (*TestCaseOtelClient) ExpectedSpanCount() int {
	if otelc.Enabled() {
		return 1
	}
	return 2
}

func (*TestCaseOtelClient) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
		},
	}
}

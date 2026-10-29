// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package goredis

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	redistrace "github.com/DataDog/dd-trace-go/contrib/redis/go-redis.v9/v2"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

// TestCaseExplicitWrap checks that when the application wraps a client the
// rules already wrapped, only the application's hook traces it, with its own
// options.
type TestCaseExplicitWrap struct {
	addr string
}

func (tc *TestCaseExplicitWrap) Setup(_ context.Context, t *testing.T) {
	if !otelc.Enabled() {
		t.Skip("orchestrion traces the client with both hooks")
	}
	tc.addr = closedAddr(t)
}

func (tc *TestCaseExplicitWrap) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	client := redis.NewClient(&redis.Options{Addr: tc.addr, MaxRetries: -1})
	redistrace.WrapClient(client, redistrace.WithService("explicit"), redistrace.WithSkipRawCommand(true))
	require.Error(t, client.Do(ctx, "get", "explicit-wrap").Err())
	require.NoError(t, client.Close())
}

// ExpectedSpanCount is test.root and the application's redis.command and redis.dial spans.
func (*TestCaseExplicitWrap) ExpectedSpanCount() int {
	return 3
}

func (*TestCaseExplicitWrap) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":     "redis.command",
						"service":  "explicit",
						"resource": "get",
					},
					Children: trace.Traces{
						{
							Tags: map[string]any{
								"name":    "redis.dial",
								"service": "explicit",
							},
						},
					},
				},
			},
		},
	}
}

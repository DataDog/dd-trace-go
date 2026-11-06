// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package goredis

import (
	"context"
	"testing"

	"example.com/goredisdep/redisv8"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseDependency checks that a client created in a dependency module, not
// in the module being built, is traced once.
type TestCaseDependency struct {
	addr string
}

func (tc *TestCaseDependency) Setup(_ context.Context, t *testing.T) {
	tc.addr = closedAddr(t)
}

func (tc *TestCaseDependency) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	client := redisv8.NewClient(tc.addr)
	require.Error(t, client.Do(ctx, "get", "dependency").Err())
	require.NoError(t, client.Close())
}

func (*TestCaseDependency) ExpectedSpanCount() int {
	return 2
}

func (*TestCaseDependency) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{
				commandSpan("redis.client", "dependency", false),
			},
		},
	}
}

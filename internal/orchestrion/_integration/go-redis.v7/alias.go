// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package goredis

import (
	"context"
	"testing"

	redisv7 "github.com/go-redis/redis/v7"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseAliasedImport checks that a client is traced when go-redis is
// imported under another name.
type TestCaseAliasedImport struct {
	addr string
}

func (tc *TestCaseAliasedImport) Setup(_ context.Context, t *testing.T) {
	tc.addr = closedAddr(t)
}

func (tc *TestCaseAliasedImport) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	client := redisv7.NewClient(&redisv7.Options{Addr: tc.addr})
	require.Error(t, client.DoContext(ctx, "get", "alias-new-client").Err())
	require.NoError(t, client.Close())
}

func (*TestCaseAliasedImport) ExpectedSpanCount() int {
	// test.root, then a redis.command span.
	return 2
}

func (*TestCaseAliasedImport) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{
				commandSpan("redis.client", "alias-new-client", false),
			},
		},
	}
}

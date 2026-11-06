// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package goredis

import (
	"context"
	"testing"

	"github.com/go-redis/redis/v7"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

// TestCaseUniversalClients checks that Cluster, Ring and universal clients
// each report exactly one span per command. go-redis builds the clients behind
// them with its own constructors, which the rules leave alone, so those must
// not report spans of their own.
type TestCaseUniversalClients struct {
	addr string
}

func (tc *TestCaseUniversalClients) Setup(_ context.Context, t *testing.T) {
	if !otelc.Enabled() {
		t.Skip("orchestrion only wraps redis.NewClient and redis.NewFailoverClient")
	}
	tc.addr = closedAddr(t)
}

func (tc *TestCaseUniversalClients) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	clients := map[string]redis.UniversalClient{
		"new-cluster-client":           redis.NewClusterClient(&redis.ClusterOptions{Addrs: []string{tc.addr}, MaxRedirects: -1}),
		"new-ring":                     redis.NewRing(&redis.RingOptions{Addrs: map[string]string{"shard": tc.addr}}),
		"new-universal-client":         redis.NewUniversalClient(&redis.UniversalOptions{Addrs: []string{tc.addr}}),
		"new-universal-cluster-client": redis.NewUniversalClient(&redis.UniversalOptions{Addrs: []string{tc.addr, tc.addr}, MaxRedirects: -1}),
	}
	for key, client := range clients {
		require.Error(t, client.DoContext(ctx, "get", key).Err())
		require.NoError(t, client.Close())
	}
}

func (*TestCaseUniversalClients) ExpectedSpanCount() int {
	// test.root, then a redis.command span per client.
	return 5
}

func (*TestCaseUniversalClients) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{
				commandSpan("redis.client", "new-cluster-client", false),
				commandSpan("redis.client", "new-ring", false),
				commandSpan("redis.client", "new-universal-client", false),
				commandSpan("redis.client", "new-universal-cluster-client", false),
			},
		},
	}
}

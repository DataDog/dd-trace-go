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

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

// TestCaseCustomNewClient pins how a Ring whose RingOptions.NewClient calls
// redis.NewClient is traced.
//
// FIXME: the rules wrap both the Ring and the shard client that the
// application's NewClient function builds, so each command reports two
// redis.command spans.
type TestCaseCustomNewClient struct {
	addr string
}

func (tc *TestCaseCustomNewClient) Setup(_ context.Context, t *testing.T) {
	if !otelc.Enabled() {
		t.Skip("orchestrion only wraps redis.NewClient and redis.NewFailoverClient")
	}
	tc.addr = closedAddr(t)
}

func (tc *TestCaseCustomNewClient) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	ring := redis.NewRing(&redis.RingOptions{
		Addrs:      map[string]string{"shard": tc.addr},
		MaxRetries: -1,
		NewClient: func(opt *redis.Options) *redis.Client {
			return redis.NewClient(opt)
		},
	})
	require.Error(t, ring.Do(ctx, "get", "custom-new-client").Err())
	require.NoError(t, ring.Close())
}

func (*TestCaseCustomNewClient) ExpectedSpanCount() int {
	return 4
}

func (*TestCaseCustomNewClient) ExpectedTraces() trace.Traces {
	ringSpan := commandSpan("redis.client", "custom-new-client", false)
	ringSpan.Children = trace.Traces{commandSpan("redis.client", "custom-new-client", true)}
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{ringSpan},
		},
	}
}

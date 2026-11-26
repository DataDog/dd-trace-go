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
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseNewClient checks that a client from rueidis.NewClient is traced
// once.
type TestCaseNewClient struct {
	addr string
}

func (tc *TestCaseNewClient) PreBootstrap(_ context.Context, t *testing.T) {
	tc.addr = startRedis(t)
}

func (*TestCaseNewClient) Setup(context.Context, *testing.T) {}

func (tc *TestCaseNewClient) Run(ctx context.Context, t *testing.T) {
	client, err := rueidis.NewClient(rueidis.ClientOption{InitAddress: []string{tc.addr}})
	require.NoError(t, err)
	defer client.Close()

	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()
	require.True(t, rueidis.IsRedisNil(client.Do(ctx, client.B().Get().Key("new-client").Build()).Error()))
}

// ExpectedSpanCount is test.root and the GET.
func (*TestCaseNewClient) ExpectedSpanCount() int {
	return 2
}

func (*TestCaseNewClient) ExpectedTraces() trace.Traces {
	return rootWithCommand("redis.client")
}

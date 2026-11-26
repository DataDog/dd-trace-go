// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package rueidis

import (
	"context"
	"testing"

	"example.com/rueidisdep"
	"github.com/redis/rueidis"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseDependency checks that a client created in a dependency module, not
// in the module being built, is traced once.
type TestCaseDependency struct {
	addr string
}

func (tc *TestCaseDependency) PreBootstrap(_ context.Context, t *testing.T) {
	tc.addr = startRedis(t)
}

func (*TestCaseDependency) Setup(context.Context, *testing.T) {}

func (tc *TestCaseDependency) Run(ctx context.Context, t *testing.T) {
	client, err := rueidisdep.NewClient(tc.addr)
	require.NoError(t, err)
	defer client.Close()

	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()
	require.True(t, rueidis.IsRedisNil(client.Do(ctx, client.B().Get().Key("dependency").Build()).Error()))
}

// ExpectedSpanCount is test.root and the GET.
func (*TestCaseDependency) ExpectedSpanCount() int {
	return 2
}

func (*TestCaseDependency) ExpectedTraces() trace.Traces {
	return rootWithCommand("redis.client")
}

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package rueidis

import (
	"context"
	"testing"

	rdb "github.com/redis/rueidis"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseAliasedImport checks that a client is traced when rueidis is
// imported under another name.
type TestCaseAliasedImport struct {
	addr string
}

func (tc *TestCaseAliasedImport) PreBootstrap(_ context.Context, t *testing.T) {
	tc.addr = startRedis(t)
}

func (*TestCaseAliasedImport) Setup(context.Context, *testing.T) {}

func (tc *TestCaseAliasedImport) Run(ctx context.Context, t *testing.T) {
	client, err := rdb.NewClient(rdb.ClientOption{InitAddress: []string{tc.addr}})
	require.NoError(t, err)
	defer client.Close()

	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()
	require.True(t, rdb.IsRedisNil(client.Do(ctx, client.B().Get().Key("alias").Build()).Error()))
}

// ExpectedSpanCount is test.root and the GET.
func (*TestCaseAliasedImport) ExpectedSpanCount() int {
	return 2
}

func (*TestCaseAliasedImport) ExpectedTraces() trace.Traces {
	return rootWithCommand("redis.client")
}

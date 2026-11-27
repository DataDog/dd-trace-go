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
	"github.com/redis/rueidis/rueidisaside"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseCacheAside checks that the client rueidisaside creates with
// rueidis.NewClient is traced once.
type TestCaseCacheAside struct {
	addr string
}

func (tc *TestCaseCacheAside) PreBootstrap(_ context.Context, t *testing.T) {
	tc.addr = startRedis(t)
}

func (*TestCaseCacheAside) Setup(context.Context, *testing.T) {}

func (tc *TestCaseCacheAside) Run(ctx context.Context, t *testing.T) {
	aside, err := rueidisaside.NewClient(rueidisaside.ClientOption{ClientOption: clientOption(tc.addr)})
	require.NoError(t, err)
	defer aside.Close()

	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()
	client := aside.Client()
	require.True(t, rueidis.IsRedisNil(client.Do(ctx, client.B().Get().Key("rueidisaside").Build()).Error()))
}

// ExpectedSpanCount is test.root and the GET.
func (*TestCaseCacheAside) ExpectedSpanCount() int {
	return 2
}

func (*TestCaseCacheAside) ExpectedTraces() trace.Traces {
	return rootWithCommand("redis.client")
}

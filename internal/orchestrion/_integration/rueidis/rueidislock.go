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
	"github.com/redis/rueidis/rueidislock"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseLocker checks that the client rueidislock creates with
// rueidis.NewClient is traced once.
type TestCaseLocker struct {
	addr string
}

func (tc *TestCaseLocker) PreBootstrap(_ context.Context, t *testing.T) {
	tc.addr = startRedis(t)
}

func (*TestCaseLocker) Setup(context.Context, *testing.T) {}

func (tc *TestCaseLocker) Run(ctx context.Context, t *testing.T) {
	locker, err := rueidislock.NewLocker(rueidislock.LockerOption{ClientOption: clientOption(tc.addr)})
	require.NoError(t, err)
	defer locker.Close()

	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()
	client := locker.Client()
	require.True(t, rueidis.IsRedisNil(client.Do(ctx, client.B().Get().Key("rueidislock").Build()).Error()))
}

// ExpectedSpanCount is test.root and the GET.
func (*TestCaseLocker) ExpectedSpanCount() int {
	return 2
}

func (*TestCaseLocker) ExpectedTraces() trace.Traces {
	return rootWithCommand("redis.client")
}

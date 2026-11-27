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

	rueidistrace "github.com/DataDog/dd-trace-go/contrib/redis/rueidis/v2"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseContribClient checks that a client the application creates with the
// contrib is traced once, with the application's options.
type TestCaseContribClient struct {
	addr string
}

func (tc *TestCaseContribClient) PreBootstrap(_ context.Context, t *testing.T) {
	tc.addr = startRedis(t)
}

func (*TestCaseContribClient) Setup(context.Context, *testing.T) {}

func (tc *TestCaseContribClient) Run(ctx context.Context, t *testing.T) {
	client, err := rueidistrace.NewClient(clientOption(tc.addr), rueidistrace.WithService("explicit"))
	require.NoError(t, err)
	defer client.Close()

	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()
	require.True(t, rueidis.IsRedisNil(client.Do(ctx, client.B().Get().Key("contrib-client").Build()).Error()))
}

// ExpectedSpanCount is test.root and the GET.
func (*TestCaseContribClient) ExpectedSpanCount() int {
	return 2
}

func (*TestCaseContribClient) ExpectedTraces() trace.Traces {
	return rootWithCommand("explicit")
}

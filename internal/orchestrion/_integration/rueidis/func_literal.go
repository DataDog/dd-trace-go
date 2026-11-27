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

// newClientFunc calls rueidis.NewClient in a function literal outside any
// function declaration.
var newClientFunc = func(addr string) (rueidis.Client, error) {
	return rueidis.NewClient(rueidis.ClientOption{InitAddress: []string{addr}})
}

// TestCaseFuncLiteral checks that a client created in a package-level function
// literal is traced once.
type TestCaseFuncLiteral struct {
	addr string
}

func (tc *TestCaseFuncLiteral) PreBootstrap(_ context.Context, t *testing.T) {
	tc.addr = startRedis(t)
}

func (*TestCaseFuncLiteral) Setup(context.Context, *testing.T) {}

func (tc *TestCaseFuncLiteral) Run(ctx context.Context, t *testing.T) {
	client, err := newClientFunc(tc.addr)
	require.NoError(t, err)
	defer client.Close()

	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()
	require.True(t, rueidis.IsRedisNil(client.Do(ctx, client.B().Get().Key("func-literal").Build()).Error()))
}

// ExpectedSpanCount is test.root and the GET.
func (*TestCaseFuncLiteral) ExpectedSpanCount() int {
	return 2
}

func (*TestCaseFuncLiteral) ExpectedTraces() trace.Traces {
	return rootWithCommand("redis.client")
}

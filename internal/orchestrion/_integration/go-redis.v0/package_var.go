// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package goredis

import (
	"context"
	"net"
	"testing"

	"github.com/go-redis/redis"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// pkgClient is created during package initialization, before the tracer
// starts.
var pkgClient = redis.NewClient(&redis.Options{Addr: initClosedAddr()})

// TestCasePackageVar checks that a client created in a package-level variable
// is traced once.
type TestCasePackageVar struct{}

func (*TestCasePackageVar) Setup(context.Context, *testing.T) {}

func (*TestCasePackageVar) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	client := pkgClient
	require.Error(t, client.WithContext(ctx).Get("package-var").Err())
	require.NoError(t, client.Close())
}

func (*TestCasePackageVar) ExpectedSpanCount() int {
	// test.root, then a redis.command span.
	return 2
}

func (*TestCasePackageVar) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{
				commandSpan("redis.client", "package-var", false),
			},
		},
	}
}

// initClosedAddr is closedAddr for package initialization, where there is no
// *testing.T.
func initClosedAddr() string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

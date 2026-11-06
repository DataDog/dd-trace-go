// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package pgx

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCasePool checks that pools built with pgxpool.New and
// pgxpool.NewWithConfig are traced once. pgxpool opens its connections with
// pgx.ConnectConfig, which must not be traced a second time.
type TestCasePool struct {
	dbURL string
}

// PreBootstrap starts the database before the tracer, so the container's calls
// to Docker stay out of the span count.
func (tc *TestCasePool) PreBootstrap(ctx context.Context, t *testing.T) {
	tc.dbURL = startPostgres(ctx, t)
}

func (*TestCasePool) Setup(context.Context, *testing.T) {}

func (tc *TestCasePool) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	pool, err := pgxpool.New(ctx, tc.dbURL)
	require.NoError(t, err)
	selectInt(ctx, t, pool, 4)
	pool.Close()

	cfg, err := pgxpool.ParseConfig(tc.dbURL)
	require.NoError(t, err)
	pool, err = pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	selectInt(ctx, t, pool, 5)
	pool.Close()
}

func (*TestCasePool) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{
				acquireSpan(),
				querySpan(selectSQL(4)),
				querySpan(selectSQL(5)),
			},
		},
	}
}

// ExpectedSpanCount is the root, and an acquire, a connect, a query and a
// prepare span per pool.
func (*TestCasePool) ExpectedSpanCount() int {
	return 9
}

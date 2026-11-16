// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package pgx

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pgxtrace "github.com/DataDog/dd-trace-go/contrib/jackc/pgx.v5/v2"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseContrib checks that a connection and a pool the application builds
// with the contrib itself are traced once, not again by the instrumentation.
type TestCaseContrib struct {
	dbURL string
}

// PreBootstrap starts the database before the tracer, so the container's calls
// to Docker stay out of the span count.
func (tc *TestCaseContrib) PreBootstrap(ctx context.Context, t *testing.T) {
	tc.dbURL = startPostgres(ctx, t)
}

func (*TestCaseContrib) Setup(context.Context, *testing.T) {}

func (tc *TestCaseContrib) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	cfg, err := pgx.ParseConfig(tc.dbURL)
	require.NoError(t, err)
	conn, err := pgxtrace.ConnectConfig(ctx, cfg)
	require.NoError(t, err)
	selectInt(ctx, t, conn, 6)
	assert.NoError(t, conn.Close(ctx))

	pool, err := pgxtrace.NewPool(ctx, tc.dbURL)
	require.NoError(t, err)
	selectInt(ctx, t, pool, 7)
	pool.Close()
}

func (*TestCaseContrib) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{
				connectSpan(),
				acquireSpan(),
				querySpan(selectSQL(6)),
				querySpan(selectSQL(7)),
			},
		},
	}
}

// ExpectedSpanCount is the root, a connect, a query and a prepare span for the
// connection, and an acquire, a connect, a query and a prepare span for the
// pool.
func (*TestCaseContrib) ExpectedSpanCount() int {
	return 8
}

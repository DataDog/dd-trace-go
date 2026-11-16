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

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseConn checks that pgx.Connect and pgx.ConnectConfig each report one
// connect span, and that queries on the connection are traced once.
type TestCaseConn struct {
	dbURL string
}

// PreBootstrap starts the database before the tracer, so the container's calls
// to Docker stay out of the span count.
func (tc *TestCaseConn) PreBootstrap(ctx context.Context, t *testing.T) {
	tc.dbURL = startPostgres(ctx, t)
}

func (*TestCaseConn) Setup(context.Context, *testing.T) {}

func (tc *TestCaseConn) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	conn, err := pgx.Connect(ctx, tc.dbURL)
	require.NoError(t, err)
	selectInt(ctx, t, conn, 2)
	assert.NoError(t, conn.Close(ctx))

	cfg, err := pgx.ParseConfig(tc.dbURL)
	require.NoError(t, err)
	conn, err = pgx.ConnectConfig(ctx, cfg)
	require.NoError(t, err)
	selectInt(ctx, t, conn, 3)
	assert.NoError(t, conn.Close(ctx))
}

func (*TestCaseConn) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{
				connectSpan(),
				querySpan(selectSQL(2)),
				querySpan(selectSQL(3)),
			},
		},
	}
}

// ExpectedSpanCount is the root, and a connect, a query and a prepare span per
// connection.
func (*TestCaseConn) ExpectedSpanCount() int {
	return 7
}

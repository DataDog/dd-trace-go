// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package pgx

import (
	"context"
	"testing"

	"example.com/pgxdep/pgxconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseDependency checks that a connection and a pool created in a
// dependency module, not in the module being built, are traced once.
type TestCaseDependency struct {
	dbURL string
}

// PreBootstrap starts the database before the tracer, so the container's calls
// to Docker stay out of the span count.
func (tc *TestCaseDependency) PreBootstrap(ctx context.Context, t *testing.T) {
	tc.dbURL = startPostgres(ctx, t)
}

func (*TestCaseDependency) Setup(context.Context, *testing.T) {}

func (tc *TestCaseDependency) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	conn, err := pgxconn.Connect(ctx, tc.dbURL)
	require.NoError(t, err)
	selectInt(ctx, t, conn, 8)
	assert.NoError(t, conn.Close(ctx))

	pool, err := pgxconn.NewPool(ctx, tc.dbURL)
	require.NoError(t, err)
	selectInt(ctx, t, pool, 9)
	pool.Close()
}

func (*TestCaseDependency) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{
				connectSpan(),
				acquireSpan(),
				querySpan(selectSQL(8)),
				querySpan(selectSQL(9)),
			},
		},
	}
}

// ExpectedSpanCount is the root, a connect, a query and a prepare span for the
// connection, and an acquire, a connect, a query and a prepare span for the
// pool.
func (*TestCaseDependency) ExpectedSpanCount() int {
	return 8
}

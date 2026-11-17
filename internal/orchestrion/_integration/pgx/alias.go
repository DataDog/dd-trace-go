// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package pgx

import (
	"context"
	"testing"

	pgxv5 "github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseAliasedImport checks that a connection is traced when pgx is
// imported under another name.
type TestCaseAliasedImport struct {
	dbURL string
}

// PreBootstrap starts the database before the tracer, so the container's calls
// to Docker stay out of the span count.
func (tc *TestCaseAliasedImport) PreBootstrap(ctx context.Context, t *testing.T) {
	tc.dbURL = startPostgres(ctx, t)
}

func (*TestCaseAliasedImport) Setup(context.Context, *testing.T) {}

func (tc *TestCaseAliasedImport) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	conn, err := pgxv5.Connect(ctx, tc.dbURL)
	require.NoError(t, err)
	selectInt(ctx, t, conn, 10)
	assert.NoError(t, conn.Close(ctx))
}

func (*TestCaseAliasedImport) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{
				connectSpan(),
				querySpan(selectSQL(10)),
			},
		},
	}
}

// ExpectedSpanCount is the root, and a connect, a query and a prepare span.
func (*TestCaseAliasedImport) ExpectedSpanCount() int {
	return 4
}

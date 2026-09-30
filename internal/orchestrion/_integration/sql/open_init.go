// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package sql

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/sql/initdb"
)

// TestCaseOpenInit checks that a database opened and pinged while Go
// initializes packages, before the tracer, is traced.
type TestCaseOpenInit struct{}

func (*TestCaseOpenInit) Setup(_ context.Context, t *testing.T) {
	require.NoError(t, initdb.OpenErr)
}

func (*TestCaseOpenInit) Run(ctx context.Context, t *testing.T) {
	_, err := initdb.DB.ExecContext(ctx, "SELECT 1")
	require.NoError(t, err)
}

func (*TestCaseOpenInit) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"resource": "SELECT 1",
				"type":     "sql",
				"name":     "sqlite3.query",
				"service":  "sqlite3.db",
			},
			Meta: map[string]string{
				"component":      "database/sql",
				"span.kind":      "client",
				"sql.query_type": "Exec",
			},
		},
	}
}

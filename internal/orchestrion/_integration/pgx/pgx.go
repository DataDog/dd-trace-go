// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package pgx

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	testpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/DataDog/dd-trace-go/internal/orchestrion/_integration/internal/containers"
	"github.com/DataDog/dd-trace-go/internal/orchestrion/_integration/internal/trace"
)

type TestCase struct {
	container *testpostgres.PostgresContainer
	conn      *pgx.Conn
}

func (tc *TestCase) Setup(ctx context.Context, t *testing.T) {
	containers.SkipIfProviderIsNotHealthy(t)

	var err error
	container, dbURL := containers.StartPostgresContainer(t)
	tc.container = container

	tc.conn, err = pgx.Connect(ctx, dbURL)
	require.NoError(t, err)
}

func (tc *TestCase) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	var x int
	err := tc.conn.QueryRow(ctx, "SELECT 1").Scan(&x)
	require.NoError(t, err)
	require.Equal(t, 1, x)
}

func (*TestCase) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":     "pgx.query",
						"service":  "postgres.db",
						"resource": "SELECT 1",
						"type":     "sql",
					},
					Meta: map[string]string{
						"component":    "jackc/pgx.v5",
						"span.kind":    "client",
						"db.system":    "postgresql",
						"db.operation": "Query",
					},
				},
			},
		},
	}
}

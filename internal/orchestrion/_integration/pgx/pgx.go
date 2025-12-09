// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package pgx

import (
	"context"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/instrumentation/testutils/containers/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tclog "github.com/testcontainers/testcontainers-go/log"
	testpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

type TestCase struct {
	container *testpostgres.PostgresContainer
	conn      *pgx.Conn
}

func (tc *TestCase) Setup(ctx context.Context, t *testing.T) {
	containers.SkipIfProviderIsNotHealthy(t)

	var err error
	tc.container, err = testpostgres.Run(ctx,
		"docker.io/postgres:16-alpine", // Change the docker pull stage in .github/workflows/orchestrion.yml if you update this
		testcontainers.WithLogger(tclog.TestLogger(t)),
		containers.WithTestLogConsumer(t),
		// https://golang.testcontainers.org/modules/postgres/#wait-strategies_1
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
			wait.ForListeningPort("5432/tcp"),
		),
	)
	containers.AssertTestContainersError(t, err)
	containers.RegisterContainerCleanup(t, tc.container)

	dbURL, err := tc.container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	tc.conn, err = pgx.Connect(ctx, dbURL)
	require.NoError(t, err)
	t.Cleanup(func() {
		// Using a new 10s-timeout context, as we may be running cleanup after the original context expired.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		assert.NoError(t, tc.conn.Close(ctx))
	})
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

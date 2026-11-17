// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package pgx

import (
	"context"
	"strconv"
	"testing"

	"github.com/DataDog/dd-trace-go/instrumentation/testutils/containers/v2"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tclog "github.com/testcontainers/testcontainers-go/log"
	testpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// startPostgres starts a PostgreSQL container for the test and returns its URL.
func startPostgres(ctx context.Context, t *testing.T) string {
	t.Helper()
	containers.SkipIfProviderIsNotHealthy(t)

	container, err := testpostgres.Run(ctx,
		containers.Image("postgres"),
		testcontainers.WithLogger(tclog.TestLogger(t)),
		containers.WithTestLogConsumer(t),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
			wait.ForListeningPort("5432/tcp"),
		),
	)
	containers.AssertTestContainersError(t, err)
	containers.RegisterContainerCleanup(t, container)

	dbURL, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	return dbURL
}

func querySpan(sql string) *trace.Trace {
	return &trace.Trace{
		Tags: map[string]any{
			"name":     "pgx.query",
			"service":  "postgres.db",
			"resource": sql,
			"type":     "sql",
		},
		Meta: map[string]string{
			"component":    "jackc/pgx.v5",
			"db.operation": "Query",
		},
		// pgx prepares the statement for its statement cache.
		Children: trace.Traces{{
			Tags: map[string]any{
				"name":     "pgx.prepare",
				"resource": sql,
			},
			Meta: map[string]string{
				"component":    "jackc/pgx.v5",
				"db.operation": "Prepare",
			},
		}},
	}
}

func connectSpan() *trace.Trace {
	return &trace.Trace{
		Tags: map[string]any{
			"name":     "pgx.connect",
			"resource": "Connect",
		},
		Meta: map[string]string{
			"component":    "jackc/pgx.v5",
			"db.operation": "Connect",
		},
	}
}

// acquireSpan expects the connection the pool opens for this acquire to be
// reported once, under it.
func acquireSpan() *trace.Trace {
	return &trace.Trace{
		Tags: map[string]any{
			"name":     "pgx.pool.acquire",
			"resource": "Acquire",
		},
		Meta: map[string]string{
			"component":    "jackc/pgx.v5",
			"db.operation": "Acquire",
		},
		Children: trace.Traces{connectSpan()},
	}
}

// querier is what *pgx.Conn and *pgxpool.Pool have in common here.
type querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// selectInt runs "SELECT <n>" on q and checks the result.
func selectInt(ctx context.Context, t *testing.T, q querier, n int) {
	t.Helper()
	var got int
	require.NoError(t, q.QueryRow(ctx, selectSQL(n)).Scan(&got))
	require.Equal(t, n, got)
}

func selectSQL(n int) string {
	return "SELECT " + strconv.Itoa(n)
}

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package pgx

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	sqltrace "github.com/DataDog/dd-trace-go/contrib/database/sql/v2"

	"github.com/DataDog/dd-trace-go/v2/appsec/events"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptracemock"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
)

const sqlInjection = "' OR 1 = 1 --"
const injectedQuery = "SELECT 1 WHERE 'safe' = '" + sqlInjection + "'"

type sqlClient interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	SendBatch(context.Context, *pgx.Batch) pgx.BatchResults
}

func startSQLAppSec(t *testing.T, enabled bool) {
	t.Helper()
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/rasp.json")
	t.Setenv("DD_APPSEC_WAF_TIMEOUT", "1s")
	if enabled {
		t.Setenv("DD_APPSEC_RASP_ENABLED", "true")
	} else {
		t.Setenv("DD_APPSEC_RASP_ENABLED", "false")
	}
	testutils.StartAppSec(t)
}

func sqlRequest(t *testing.T, run func(context.Context), status int) *mocktracer.Span {
	t.Helper()
	mt := mocktracer.Start()
	defer mt.Stop()
	mux := httptracemock.NewServeMux()
	mux.HandleFunc("/query", func(_ http.ResponseWriter, r *http.Request) {
		run(r.Context())
	})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/query?input="+url.QueryEscape(sqlInjection), nil))
	require.Equal(t, status, w.Code)
	for _, span := range mt.FinishedSpans() {
		if span.OperationName() == "http.request" {
			return span
		}
	}
	t.Fatal("missing HTTP span")
	return nil
}

func TestRASPSQLMonitoring(t *testing.T) {
	startSQLAppSec(t, true)
	for _, traceEnabled := range []bool{true, false} {
		opts := []Option{WithTraceQuery(traceEnabled), WithTraceBatch(traceEnabled)}
		conn, err := Connect(context.Background(), postgresDSN, opts...)
		require.NoError(t, err)
		t.Cleanup(func() { conn.Close(context.Background()) })
		pool, err := NewPool(context.Background(), postgresDSN, opts...)
		require.NoError(t, err)
		t.Cleanup(pool.Close)
		tx, err := pool.Begin(context.Background())
		require.NoError(t, err)
		t.Cleanup(func() { tx.Rollback(context.Background()) })
		for name, client := range map[string]sqlClient{"conn": conn, "pool": pool, "tx": tx} {
			for operation, run := range map[string]func(*testing.T, context.Context){
				"exec": func(t *testing.T, ctx context.Context) {
					tag, err := client.Exec(ctx, injectedQuery)
					require.NoError(t, err)
					require.EqualValues(t, 1, tag.RowsAffected())
				},
				"query": func(t *testing.T, ctx context.Context) {
					rows, err := client.Query(ctx, injectedQuery)
					require.NoError(t, err)
					defer rows.Close()
					require.True(t, rows.Next())
					require.NoError(t, rows.Err())
				},
				"query-row": func(t *testing.T, ctx context.Context) {
					var value int
					require.NoError(t, client.QueryRow(ctx, injectedQuery).Scan(&value))
					require.Equal(t, 1, value)
				},
				"batch": func(t *testing.T, ctx context.Context) {
					batch := &pgx.Batch{}
					batch.Queue(injectedQuery)
					br := client.SendBatch(ctx, batch)
					var value int
					require.NoError(t, br.QueryRow().Scan(&value))
					require.NoError(t, br.Close())
					require.Equal(t, 1, value)
				},
			} {
				t.Run(name+"/"+operation+"/tracing="+strconv.FormatBool(traceEnabled), func(t *testing.T) {
					span := sqlRequest(t, func(ctx context.Context) { run(t, ctx) }, http.StatusOK)
					require.Contains(t, span.Tag("_dd.appsec.json"), "rasp-942-100")
					require.EqualValues(t, 1, span.Tag("_dd.appsec.rasp.rule.eval"))
					require.Nil(t, span.Tag("appsec.blocked"))
				})
			}
		}
	}
}

func TestRASPSQLMonitoringParameterized(t *testing.T) {
	startSQLAppSec(t, true)
	conn, err := Connect(context.Background(), postgresDSN)
	require.NoError(t, err)
	defer conn.Close(context.Background())
	span := sqlRequest(t, func(ctx context.Context) {
		var value string
		require.NoError(t, conn.QueryRow(ctx, "SELECT $1::text", sqlInjection).Scan(&value))
		require.Equal(t, sqlInjection, value)
	}, http.StatusOK)
	require.Nil(t, span.Tag("_dd.appsec.json"))
	require.EqualValues(t, 1, span.Tag("_dd.appsec.rasp.rule.eval"))
}

func TestRASPSQLMonitoringDisabled(t *testing.T) {
	startSQLAppSec(t, false)
	conn, err := Connect(context.Background(), postgresDSN)
	require.NoError(t, err)
	defer conn.Close(context.Background())
	span := sqlRequest(t, func(ctx context.Context) {
		_, err := conn.Exec(ctx, injectedQuery)
		require.NoError(t, err)
	}, http.StatusOK)
	require.Nil(t, span.Tag("_dd.appsec.json"))
	require.Nil(t, span.Tag("_dd.appsec.rasp.rule.eval"))
}

func TestRASPSQLMonitoringBatchStatements(t *testing.T) {
	startSQLAppSec(t, true)
	conn, err := Connect(context.Background(), postgresDSN)
	require.NoError(t, err)
	defer conn.Close(context.Background())
	span := sqlRequest(t, func(ctx context.Context) {
		batch := &pgx.Batch{}
		batch.Queue("SELECT 1")
		batch.Queue(injectedQuery)
		batch.Queue(injectedQuery)
		br := conn.SendBatch(ctx, batch)
		defer br.Close()
		for range 3 {
			var value int
			require.NoError(t, br.QueryRow().Scan(&value))
			require.Equal(t, 1, value)
		}
		require.NoError(t, br.Close())
	}, http.StatusOK)
	require.Contains(t, span.Tag("_dd.appsec.json"), "rasp-942-100")
	require.EqualValues(t, 3, span.Tag("_dd.appsec.rasp.rule.eval"))
	require.Nil(t, span.Tag("appsec.blocked"))
}

func TestRASPSQLMonitoringDatabaseSQL(t *testing.T) {
	startSQLAppSec(t, true)
	for _, wrapped := range []bool{false, true} {
		cfg, err := pgx.ParseConfig(postgresDSN)
		require.NoError(t, err)
		cfg.Tracer = wrapPgxTracer(cfg)
		connector := stdlib.GetConnector(*cfg)
		var db *sql.DB
		if wrapped {
			db = sqltrace.OpenDB(connector, sqltrace.WithDBMPropagation(tracer.DBMPropagationModeService))
		} else {
			db = sql.OpenDB(connector)
		}
		t.Cleanup(func() { db.Close() })
		conn, err := Connect(context.Background(), postgresDSN)
		require.NoError(t, err)
		t.Cleanup(func() { conn.Close(context.Background()) })
		span := sqlRequest(t, func(ctx context.Context) {
			_, err := db.ExecContext(ctx, "SELECT 1")
			require.NoError(t, err)
			rows, err := db.QueryContext(ctx, "SELECT 1")
			require.NoError(t, err)
			require.NoError(t, rows.Close())
			// The marker must not suppress another query using the original request context.
			_, err = conn.Exec(ctx, "SELECT 1")
			require.NoError(t, err)
		}, http.StatusOK)
		require.EqualValues(t, 3, span.Tag("_dd.appsec.rasp.rule.eval"))
		if wrapped {
			span = sqlRequest(t, func(ctx context.Context) {
				_, err := db.ExecContext(ctx, injectedQuery)
				require.True(t, events.IsSecurityError(err))
			}, http.StatusForbidden)
			require.Contains(t, span.Tag("_dd.appsec.json"), "rasp-942-100")
			require.EqualValues(t, 1, span.Tag("_dd.appsec.rasp.rule.eval"))
		}
	}
}

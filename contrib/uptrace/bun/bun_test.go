// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package bun

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "github.com/microsoft/go-mssqldb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	_ "modernc.org/sqlite"
)

func TestMain(m *testing.M) {
	_, ok := os.LookupEnv("INTEGRATION")
	if !ok {
		fmt.Println("--- SKIP: to enable integration test, set the INTEGRATION environment variable")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func setupDB(t *testing.T, driverName, dataSourceName string, opts ...Option) *bun.DB {
	t.Helper()
	sqlite, err := sql.Open(driverName, dataSourceName)
	if err != nil {
		t.Fatal(err)
	}

	db := bun.NewDB(sqlite, sqlitedialect.New())
	Wrap(db, opts...)

	return db
}

func TestImplementsHook(_ *testing.T) {
	var _ bun.QueryHook = (*queryHook)(nil)
}

func TestSelect(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	tC := []struct {
		name       string
		driver     string
		dataSource string
		expected   string
	}{
		{
			name:       "SQLite",
			driver:     "sqlite",
			dataSource: "file::memory:?cache=shared",
			expected:   ext.DBSystemOtherSQL,
		},
		{
			name:       "Postgres",
			driver:     "postgres",
			dataSource: "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable",
			expected:   ext.DBSystemPostgreSQL,
		},
		{
			name:       "MySQL",
			driver:     "mysql",
			dataSource: "test:test@tcp(127.0.0.1:3306)/test",
			expected:   ext.DBSystemMySQL,
		},
		{
			name:       "MSSQL",
			driver:     "sqlserver",
			dataSource: "sqlserver://sa:myPassw0rd@127.0.0.1:1433?database=master",
			expected:   ext.DBSystemMicrosoftSQLServer,
		},
	}
	for _, tt := range tC {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			db := setupDB(t, tt.driver, tt.dataSource)
			parentSpan, ctx := tracer.StartSpanFromContext(context.Background(), "http.request",
				tracer.ServiceName("fake-http-server"),
				tracer.SpanType(ext.SpanTypeWeb),
			)

			var n, rows int64
			res, err := db.NewSelect().ColumnExpr("1").Exec(ctx, &n)
			parentSpan.Finish()
			spans := mt.FinishedSpans()

			require.NoError(t, err)
			rows, _ = res.RowsAffected()
			assert.Equal(int64(1), rows)
			assert.Equal(2, len(spans))
			assert.Equal(nil, err)
			assert.Equal(int64(1), n)
			assert.Equal("bun.query", spans[0].OperationName())
			assert.Equal("http.request", spans[1].OperationName())
			assert.Equal("uptrace/bun", spans[0].Tag(ext.Component))
			assert.Equal(ext.DBSystemOtherSQL, spans[0].Tag(ext.DBSystem))
			mt.Reset()
		})
	}
}

func TestServiceName(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		db := setupDB(t, "sqlite", "file::memory:?cache=shared")
		parentSpan, ctx := tracer.StartSpanFromContext(context.Background(), "http.request",
			tracer.ServiceName("fake-http-server"),
			tracer.SpanType(ext.SpanTypeWeb),
		)

		var n int
		res, err := db.NewSelect().ColumnExpr("1").Exec(ctx, &n)
		parentSpan.Finish()
		spans := mt.FinishedSpans()

		require.NoError(t, err)
		rows, _ := res.RowsAffected()
		assert.Equal(int64(1), rows)
		assert.Len(spans, 2)
		assert.Equal(nil, err)
		assert.Equal(1, n)
		assert.Equal("bun.query", spans[0].OperationName())
		assert.Equal("http.request", spans[1].OperationName())
		assert.Equal("bun.db", spans[0].Tag(ext.ServiceName))
		assert.Equal("fake-http-server", spans[1].Tag(ext.ServiceName))
		assert.Equal("uptrace/bun", spans[0].Tag(ext.Component))
		assert.Equal(ext.DBSystemOtherSQL, spans[0].Tag(ext.DBSystem))
		assert.Equal(spans[0].ParentID(), spans[1].SpanID())
	})

	t.Run("global", func(t *testing.T) {
		prevName := globalconfig.ServiceName()
		defer globalconfig.SetServiceName(prevName)
		globalconfig.SetServiceName("global-service")

		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		db := setupDB(t, "sqlite", "file::memory:?cache=shared")
		parentSpan, ctx := tracer.StartSpanFromContext(context.Background(), "http.request",
			tracer.ServiceName("fake-http-server"),
			tracer.SpanType(ext.SpanTypeWeb),
		)

		var n int
		res, err := db.NewSelect().ColumnExpr("1").Exec(ctx, &n)
		parentSpan.Finish()
		spans := mt.FinishedSpans()

		require.NoError(t, err)
		rows, _ := res.RowsAffected()
		assert.Equal(int64(1), rows)
		assert.Equal(2, len(spans))
		assert.Equal(nil, err)
		assert.Equal(1, n)
		assert.Equal("bun.query", spans[0].OperationName())
		assert.Equal("http.request", spans[1].OperationName())
		assert.Equal("global-service", spans[0].Tag(ext.ServiceName))
		assert.Equal("fake-http-server", spans[1].Tag(ext.ServiceName))
		assert.Equal("uptrace/bun", spans[0].Tag(ext.Component))
		assert.Equal(ext.DBSystemOtherSQL, spans[0].Tag(ext.DBSystem))
	})

	t.Run("custom", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		db := setupDB(t, "sqlite", "file::memory:?cache=shared", WithService("my-service-name"))
		parentSpan, ctx := tracer.StartSpanFromContext(context.Background(), "http.request",
			tracer.ServiceName("fake-http-server"),
			tracer.SpanType(ext.SpanTypeWeb),
		)

		var n int
		res, err := db.NewSelect().ColumnExpr("1").Exec(ctx, &n)
		parentSpan.Finish()
		spans := mt.FinishedSpans()

		require.NoError(t, err)
		rows, _ := res.RowsAffected()
		assert.Equal(int64(1), rows)
		assert.Equal(2, len(spans))
		assert.Equal(nil, err)
		assert.Equal(1, n)
		assert.Equal("bun.query", spans[0].OperationName())
		assert.Equal("http.request", spans[1].OperationName())
		assert.Equal("my-service-name", spans[0].Tag(ext.ServiceName))
		assert.Equal("fake-http-server", spans[1].Tag(ext.ServiceName))
		assert.Equal("uptrace/bun", spans[0].Tag(ext.Component))
		assert.Equal(ext.DBSystemOtherSQL, spans[0].Tag(ext.DBSystem))
	})
}

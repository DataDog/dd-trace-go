// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"log"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/contrib/database/sql/v2/internal"

	"github.com/DataDog/dd-trace-go/v2/appsec/events"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"

	"github.com/go-sql-driver/mysql"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTryTraceOTelSemantics(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	tp := &traceParams{
		driverName: "mysql",
		cfg: &config{
			serviceName:   "mysql.db",
			spanName:      "mysql.query",
			analyticsRate: math.NaN(),
			otelSemantics: true,
			tags: map[string]any{
				"custom":         "connection",
				ext.DBSystemName: "invalid",
			},
		},
		connectionInfo: internal.ConnectionInfo{
			System:    "mysql",
			User:      "alice",
			Namespace: "orders",
			Host:      "db.example.com",
			Port:      "3307",
		},
	}
	tp.datadogTags = tp.connectionInfo.DatadogTags()
	ctx := WithSpanTags(context.Background(), map[string]string{
		"custom":         "operation",
		ext.DBSystemName: "also-invalid",
	})
	const query = "SELECT * FROM customer"
	tp.tryTrace(ctx, QueryTypeQuery, query, time.Now(), nil)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	span := spans[0]
	assert.Equal(t, "orders", span.Tag(ext.ResourceName))
	assert.Equal(t, query, span.Tag(ext.DBStatement))
	assert.Equal(t, ext.DBSystemMySQL, span.Tag(ext.DBSystemName))
	assert.Equal(t, "orders", span.Tag(ext.DBNamespace))
	assert.Equal(t, "db.example.com", span.Tag(ext.ServerAddress))
	assert.Equal(t, float64(3307), span.Tag(ext.ServerPort))
	assert.Equal(t, "alice", span.Tag(ext.DBUser))
	assert.Equal(t, "operation", span.Tag("custom"))
	assert.Nil(t, span.Tag(ext.DBSystem))
	assert.Nil(t, span.Tag(ext.DBName))
	assert.Nil(t, span.Tag(ext.TargetHost))
	assert.Nil(t, span.Tag(ext.TargetPort))
}

func TestTryTraceOTelErrorSemantics(t *testing.T) {
	tests := []struct {
		name      string
		errCheck  func(error) bool
		wantError bool
	}{
		{name: "accepted", wantError: true},
		{name: "filtered", errCheck: func(error) bool { return false }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			var tags map[string]any
			if tt.wantError {
				tags = map[string]any{
					ext.ErrorType:            "invalid",
					ext.DBResponseStatusCode: "invalid",
				}
			}
			tp := &traceParams{
				driverName: "mysql",
				cfg: &config{
					serviceName:   "mysql.db",
					spanName:      "mysql.query",
					analyticsRate: math.NaN(),
					otelSemantics: true,
					errCheck:      tt.errCheck,
					tags:          tags,
				},
				connectionInfo: internal.ConnectionInfo{System: "mysql"},
			}
			err := &mysql.MySQLError{Number: 1062, Message: "duplicate entry"}
			tp.tryTrace(context.Background(), QueryTypeExec, "INSERT INTO customer VALUES (1)", time.Now(), err)

			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)
			span := spans[0]
			if tt.wantError {
				assert.Equal(t, err.Error(), span.Tag(ext.ErrorMsg))
				assert.Equal(t, "1062", span.Tag(ext.ErrorType))
				assert.Equal(t, "1062", span.Tag(ext.DBResponseStatusCode))
				return
			}
			assert.Nil(t, span.Tag(ext.ErrorMsg))
			assert.Nil(t, span.Tag(ext.ErrorType))
			assert.Nil(t, span.Tag(ext.DBResponseStatusCode))
		})
	}
}

func TestTryTraceOTelSpecialErrors(t *testing.T) {
	tp := &traceParams{
		driverName: "mysql",
		cfg: &config{
			serviceName:   "mysql.db",
			spanName:      "mysql.query",
			analyticsRate: math.NaN(),
			otelSemantics: true,
		},
		connectionInfo: internal.ConnectionInfo{System: "mysql"},
	}

	t.Run("driver skip", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		tp.tryTrace(context.Background(), QueryTypeExec, "SELECT 1", time.Now(), driver.ErrSkip)
		assert.Empty(t, mt.FinishedSpans())
	})
	t.Run("security error", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		tp.tryTrace(context.Background(), QueryTypeExec, "SELECT 1", time.Now(), &events.BlockingSecurityEvent{})
		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		assert.Nil(t, spans[0].Tag(ext.ErrorMsg))
		assert.Nil(t, spans[0].Tag(ext.ErrorType))
		assert.Nil(t, spans[0].Tag(ext.DBResponseStatusCode))
	})
}

func TestWithSpanTags(t *testing.T) {
	type sqlRegister struct {
		name   string
		dsn    string
		driver driver.Driver
		opts   []Option
	}
	type want struct {
		opName   string
		ctxTags  map[string]string
		dbSystem string
	}
	testcases := []struct {
		name        string
		sqlRegister sqlRegister
		want        want
	}{
		{
			name: "mysql",
			sqlRegister: sqlRegister{
				name:   "mysql",
				dsn:    "test:test@tcp(127.0.0.1:3306)/test",
				driver: &mysql.MySQLDriver{},
				opts:   []Option{},
			},
			want: want{
				opName: "mysql.query",
				ctxTags: map[string]string{
					"mysql_tag1": "mysql_value1",
					"mysql_tag2": "mysql_value2",
					"mysql_tag3": "mysql_value3",
				},
				dbSystem: "mysql",
			},
		},
		{
			name: "postgres",
			sqlRegister: sqlRegister{
				name:   "postgres",
				dsn:    "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable",
				driver: &pq.Driver{},
				opts: []Option{
					WithService("postgres-test"),
					WithAnalyticsRate(0.2),
				},
			},
			want: want{
				opName: "postgres.query",
				ctxTags: map[string]string{
					"pg_tag1": "pg_value1",
					"pg_tag2": "pg_value2",
				},
				dbSystem: "postgresql",
			},
		},
	}
	mt := mocktracer.Start()
	defer mt.Stop()
	for _, tt := range testcases {
		t.Run(tt.name, func(t *testing.T) {
			Register(tt.sqlRegister.name, tt.sqlRegister.driver, tt.sqlRegister.opts...)
			defer unregister(tt.sqlRegister.name)
			db, err := Open(tt.sqlRegister.name, tt.sqlRegister.dsn)
			if err != nil {
				log.Fatal(err)
			}
			defer db.Close()
			mt.Reset()

			ctx := WithSpanTags(context.Background(), tt.want.ctxTags)

			rows, err := db.QueryContext(ctx, "SELECT 1")
			assert.NoError(t, err)
			rows.Close()

			spans := mt.FinishedSpans()
			assert.Len(t, spans, 2)

			connectSpan := spans[0]
			assert.Equal(t, tt.want.opName, connectSpan.OperationName())
			assert.Equal(t, "Connect", connectSpan.Tag("sql.query_type"))
			for k, v := range tt.want.ctxTags {
				assert.Equal(t, v, connectSpan.Tag(k), "Value mismatch on tag %s", k)
			}
			assert.Equal(t, ext.SpanKindClient, connectSpan.Tag(ext.SpanKind))
			assert.Equal(t, "database/sql", connectSpan.Tag(ext.Component))
			assert.Equal(t, string(componentName), connectSpan.Integration())
			assert.Equal(t, tt.want.dbSystem, connectSpan.Tag(ext.DBSystem))

			span := spans[1]
			assert.Equal(t, tt.want.opName, span.OperationName())
			for k, v := range tt.want.ctxTags {
				assert.Equal(t, v, span.Tag(k), "Value mismatch on tag %s", k)
			}
			assert.Equal(t, ext.SpanKindClient, span.Tag(ext.SpanKind))
			assert.Equal(t, "database/sql", span.Tag(ext.Component))
			assert.Equal(t, string(componentName), connectSpan.Integration())
			assert.Equal(t, tt.want.dbSystem, connectSpan.Tag(ext.DBSystem))
		})
	}
}

func TestWithIgnoreQueryTypes(t *testing.T) {
	type sqlRegister struct {
		name   string
		dsn    string
		driver driver.Driver
		opts   []Option
	}
	testcases := []struct {
		name         string
		sqlRegister  sqlRegister
		dbOp         func(t *testing.T, db *sql.DB)
		wantNumSpans int
	}{
		{
			name: "mysql/select/ignore-connect",
			sqlRegister: sqlRegister{
				name:   "mysql",
				dsn:    "test:test@tcp(127.0.0.1:3306)/test",
				driver: &mysql.MySQLDriver{},
				opts: []Option{
					WithIgnoreQueryTypes(QueryTypeConnect),
				},
			},
			dbOp: func(t *testing.T, db *sql.DB) {
				ctx := context.Background()
				rows, err := db.QueryContext(ctx, "SELECT 1")
				require.NoError(t, err)
				rows.Close()
			},
			wantNumSpans: 1,
		},
		{
			name: "postgres/select/ignore-connect",
			sqlRegister: sqlRegister{
				name:   "postgres",
				dsn:    "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable",
				driver: &pq.Driver{},
				opts: []Option{
					WithIgnoreQueryTypes(QueryTypeConnect),
				},
			},
			dbOp: func(t *testing.T, db *sql.DB) {
				ctx := context.Background()
				rows, err := db.QueryContext(ctx, "SELECT 1")
				require.NoError(t, err)
				rows.Close()
			},
			wantNumSpans: 1,
		},
	}
	mt := mocktracer.Start()
	defer mt.Stop()
	for _, tt := range testcases {
		t.Run(tt.name, func(t *testing.T) {
			Register(tt.sqlRegister.name, tt.sqlRegister.driver, tt.sqlRegister.opts...)
			defer unregister(tt.sqlRegister.name)
			db, err := Open(tt.sqlRegister.name, tt.sqlRegister.dsn)
			require.NoError(t, err)
			defer db.Close()
			mt.Reset()

			tt.dbOp(t, db)

			spans := mt.FinishedSpans()
			assert.Len(t, spans, tt.wantNumSpans)
		})
	}
}

func TestWithChildSpansOnly(t *testing.T) {
	type sqlRegister struct {
		name   string
		dsn    string
		driver driver.Driver
		opts   []Option
	}
	testcases := []struct {
		name        string
		sqlRegister sqlRegister
	}{
		{
			name: "mysql",
			sqlRegister: sqlRegister{
				name:   "mysql",
				dsn:    "test:test@tcp(127.0.0.1:3306)/test",
				driver: &mysql.MySQLDriver{},
				opts: []Option{
					WithChildSpansOnly(),
				},
			},
		},
		{
			name: "postgres",
			sqlRegister: sqlRegister{
				name:   "postgres",
				dsn:    "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable",
				driver: &pq.Driver{},
				opts: []Option{
					WithChildSpansOnly(),
					WithService("postgres-test"),
					WithAnalyticsRate(0.2),
				},
			},
		},
	}
	mt := mocktracer.Start()
	defer mt.Stop()
	for _, tt := range testcases {
		t.Run(tt.name, func(t *testing.T) {
			Register(tt.sqlRegister.name, tt.sqlRegister.driver, tt.sqlRegister.opts...)
			defer unregister(tt.sqlRegister.name)
			db, err := Open(tt.sqlRegister.name, tt.sqlRegister.dsn)
			require.NoError(t, err)
			defer db.Close()
			mt.Reset()

			ctx := context.Background()

			rows, err := db.QueryContext(ctx, "SELECT 1")
			require.NoError(t, err)
			rows.Close()

			spans := mt.FinishedSpans()
			assert.Len(t, spans, 0)
		})
	}
}

func TestWithErrorCheck(t *testing.T) {
	testOpts := func(errExist bool, opts ...Option) func(t *testing.T) {
		return func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			Register("mysql", &mysql.MySQLDriver{})
			defer unregister("mysql")

			db, err := Open("mysql", "test:test@tcp(127.0.0.1:3306)/test", opts...)
			if err != nil {
				log.Fatal(err)
			}
			defer db.Close()

			db.QueryContext(context.Background(), "SELECT a FROM "+tableName)

			spans := mt.FinishedSpans()
			assert.True(t, len(spans) > 0)

			s := spans[len(spans)-1]
			assert.Equal(t, errExist, s.Tag(ext.ErrorMsg) != nil)
		}
	}

	t.Run("defaults", testOpts(true))
	t.Run("errcheck", testOpts(false, WithErrorCheck(func(err error) bool {
		return !strings.Contains(err.Error(), `Unknown column 'a' in 'field list'`)
	})))

}

func TestWithCustomTag(t *testing.T) {
	type sqlRegister struct {
		name   string
		dsn    string
		driver driver.Driver
	}
	type want struct {
		opName     string
		customTags map[string]interface{}
		dbSystem   string
	}
	testcases := []struct {
		name        string
		sqlRegister sqlRegister
		want        want
		options     []Option
	}{
		{
			name: "mysql",
			sqlRegister: sqlRegister{
				name:   "mysql",
				dsn:    "test:test@tcp(127.0.0.1:3306)/test",
				driver: &mysql.MySQLDriver{},
			},
			want: want{
				opName: "mysql.query",
				customTags: map[string]interface{}{
					"foo": "bar",
					"baz": float64(123),
				},
				dbSystem: ext.DBSystemMySQL,
			},
			options: []Option{
				WithCustomTag("foo", "bar"),
				WithCustomTag("baz", 123),
			},
		},
		{
			name: "postgres",
			sqlRegister: sqlRegister{
				name:   "postgres",
				dsn:    "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable",
				driver: &pq.Driver{},
			},
			want: want{
				opName: "postgres.query",
				customTags: map[string]interface{}{
					"foo": "bar",
					"baz": float64(123),
				},
				dbSystem: "postgresql",
			},
			options: []Option{
				WithCustomTag("foo", "bar"),
				WithCustomTag("baz", 123),
			},
		},
	}
	mt := mocktracer.Start()
	defer mt.Stop()
	for _, tt := range testcases {
		t.Run(tt.name, func(t *testing.T) {
			Register(tt.sqlRegister.name, tt.sqlRegister.driver, tt.options...)
			defer unregister(tt.sqlRegister.name)
			db, err := Open(tt.sqlRegister.name, tt.sqlRegister.dsn, tt.options...)
			if err != nil {
				log.Fatal(err)
			}
			defer db.Close()
			mt.Reset()

			rows, err := db.QueryContext(context.Background(), "SELECT 1")
			assert.NoError(t, err)
			rows.Close()

			spans := mt.FinishedSpans()
			assert.Len(t, spans, 2)

			connectSpan := spans[0]
			assert.Equal(t, tt.want.opName, connectSpan.OperationName())
			assert.Equal(t, "Connect", connectSpan.Tag("sql.query_type"))
			for k, v := range tt.want.customTags {
				assert.Equal(t, v, connectSpan.Tag(k), "Value mismatch on tag %s", k)
			}
			assert.Equal(t, ext.SpanKindClient, connectSpan.Tag(ext.SpanKind))
			assert.Equal(t, "database/sql", connectSpan.Tag(ext.Component))
			assert.Equal(t, string(componentName), connectSpan.Integration())
			assert.Equal(t, tt.want.dbSystem, connectSpan.Tag(ext.DBSystem))

			span := spans[1]
			assert.Equal(t, tt.want.opName, span.OperationName())
			for k, v := range tt.want.customTags {
				assert.Equal(t, v, span.Tag(k), "Value mismatch on tag %s", k)
			}
			assert.Equal(t, ext.SpanKindClient, connectSpan.Tag(ext.SpanKind))
			assert.Equal(t, "database/sql", connectSpan.Tag(ext.Component))
			assert.Equal(t, string(componentName), connectSpan.Integration())
			assert.Equal(t, tt.want.dbSystem, connectSpan.Tag(ext.DBSystem))
		})
	}
}

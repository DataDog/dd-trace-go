// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sql

import (
	"context"
	"database/sql/driver"
	"log"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"

	"github.com/go-sql-driver/mysql"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithSpanTags(t *testing.T) {
	type sqlRegister struct {
		name   string
		dsn    string
		driver driver.Driver
		opts   []RegisterOption
	}
	type want struct {
		opName  string
		ctxTags map[string]string
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
				opts:   []RegisterOption{},
			},
			want: want{
				opName: "mysql.query",
				ctxTags: map[string]string{
					"mysql_tag1": "mysql_value1",
					"mysql_tag2": "mysql_value2",
					"mysql_tag3": "mysql_value3",
				},
			},
		},
		{
			name: "postgres",
			sqlRegister: sqlRegister{
				name:   "postgres",
				dsn:    "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable",
				driver: &pq.Driver{},
				opts: []RegisterOption{
					WithServiceName("postgres-test"),
					WithAnalyticsRate(0.2),
				},
			},
			want: want{
				opName: "postgres.query",
				ctxTags: map[string]string{
					"pg_tag1": "pg_value1",
					"pg_tag2": "pg_value2",
				},
			},
		},
	}
	mt := mocktracer.Start()
	defer mt.Stop()
	for _, tt := range testcases {
		t.Run(tt.name, func(t *testing.T) {
			Register(tt.sqlRegister.name, tt.sqlRegister.driver, tt.sqlRegister.opts...)
			defer unregister(tt.sqlRegister.driver)
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
			assert.Len(t, spans, 1)

			span := spans[0]
			assert.Equal(t, tt.want.opName, span.OperationName())
			for k, v := range tt.want.ctxTags {
				assert.Equal(t, v, span.Tag(k), "Value mismatch on tag %s", k)
			}
		})
	}
}

func TestWithChildSpansOnly(t *testing.T) {
	type sqlRegister struct {
		name   string
		dsn    string
		driver driver.Driver
		opts   []RegisterOption
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
				opts: []RegisterOption{
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
				opts: []RegisterOption{
					WithChildSpansOnly(),
					WithServiceName("postgres-test"),
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
			defer unregister(tt.sqlRegister.driver)
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

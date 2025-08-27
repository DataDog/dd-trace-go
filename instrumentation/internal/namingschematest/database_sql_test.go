// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"database/sql/driver"
	"testing"

	mssql "github.com/denisenkom/go-mssqldb"
	"github.com/go-sql-driver/mysql"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sqltrace "github.com/DataDog/dd-trace-go/contrib/database/sql/v2"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2/harness"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

func dbSQLGenSpans(driverName string, registerOverride bool) harness.GenSpansFn {
	return func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		var registerOpts []sqltrace.Option
		// serviceOverride has higher priority than the registerOverride parameter.
		if serviceOverride != "" {
			registerOpts = append(registerOpts, sqltrace.WithService(serviceOverride))
		} else if registerOverride {
			registerOpts = append(registerOpts, sqltrace.WithService("register-override"))
		}
		var openOpts []sqltrace.Option
		if serviceOverride != "" {
			openOpts = append(openOpts, sqltrace.WithService(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		var (
			dv  driver.Driver
			dsn string
		)
		switch driverName {
		case "sqlserver":
			dv = &mssql.Driver{}
			dsn = "sqlserver://sa:myPassw0rd@127.0.0.1:1433?database=master"
		case "postgres":
			dv = &pq.Driver{}
			dsn = "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable"
		case "mysql":
			dv = &mysql.MySQLDriver{}
			dsn = "test:test@tcp(127.0.0.1:3306)/test"
		default:
			t.Fatal("unknown driver: ", driverName)
		}
		sqltrace.Register(driverName, dv, registerOpts...)
		db, err := sqltrace.Open(driverName, dsn, openOpts...)
		require.NoError(t, err)

		err = db.Ping()
		require.NoError(t, err)

		spans := mt.FinishedSpans()
		require.Len(t, spans, 2)
		return spans
	}
}

var databaseSQL_SQLServer = harness.TestCase{
	Name:     instrumentation.PackageDatabaseSQL + "_SQLServer",
	GenSpans: dbSQLGenSpans("sqlserver", false),
	WantServiceNameV0: harness.ServiceNameAssertions{
		Defaults:        []string{"sqlserver.db", "sqlserver.db"},
		DDService:       []string{"sqlserver.db", "sqlserver.db"},
		ServiceOverride: []string{harness.TestServiceOverride, harness.TestServiceOverride},
	},
	AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 2)
		assert.Equal(t, "sqlserver.query", spans[0].OperationName())
		assert.Equal(t, "sqlserver.query", spans[1].OperationName())
	},
	AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 2)
		assert.Equal(t, "mssql.query", spans[0].OperationName())
		assert.Equal(t, "mssql.query", spans[1].OperationName())
	},
}

var databaseSQL_Postgres = harness.TestCase{
	Name:     instrumentation.PackageDatabaseSQL + "_Postgres",
	GenSpans: dbSQLGenSpans("postgres", false),
	WantServiceNameV0: harness.ServiceNameAssertions{
		Defaults:        []string{"postgres.db", "postgres.db"},
		DDService:       []string{"postgres.db", "postgres.db"},
		ServiceOverride: []string{harness.TestServiceOverride, harness.TestServiceOverride},
	},
	AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 2)
		assert.Equal(t, "postgres.query", spans[0].OperationName())
		assert.Equal(t, "postgres.query", spans[1].OperationName())
	},
	AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 2)
		assert.Equal(t, "postgresql.query", spans[0].OperationName())
		assert.Equal(t, "postgresql.query", spans[1].OperationName())
	},
}

var databaseSQL_PostgresWithRegisterOverride = harness.TestCase{
	Name:     instrumentation.PackageDatabaseSQL + "_PostgresWithRegisterOverride",
	GenSpans: dbSQLGenSpans("postgres", true),
	WantServiceNameV0: harness.ServiceNameAssertions{
		// when the WithService option is set during Register and not providing a service name when opening
		// the DB connection, that value is used as default instead of postgres.db.
		Defaults: []string{"register-override", "register-override"},
		// in v0, DD_SERVICE is ignored for this integration.
		DDService:       []string{"register-override", "register-override"},
		ServiceOverride: []string{harness.TestServiceOverride, harness.TestServiceOverride},
	},
	AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 2)
		assert.Equal(t, "postgres.query", spans[0].OperationName())
		assert.Equal(t, "postgres.query", spans[1].OperationName())
	},
	AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 2)
		assert.Equal(t, "postgresql.query", spans[0].OperationName())
		assert.Equal(t, "postgresql.query", spans[1].OperationName())
	},
}

var databaseSQL_MySQL = harness.TestCase{
	Name:     instrumentation.PackageDatabaseSQL + "_MySQL",
	GenSpans: dbSQLGenSpans("mysql", false),
	WantServiceNameV0: harness.ServiceNameAssertions{
		Defaults:        []string{"mysql.db", "mysql.db"},
		DDService:       []string{"mysql.db", "mysql.db"},
		ServiceOverride: []string{harness.TestServiceOverride, harness.TestServiceOverride},
	},
	AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 2)
		assert.Equal(t, "mysql.query", spans[0].OperationName())
		assert.Equal(t, "mysql.query", spans[1].OperationName())
	},
	AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 2)
		assert.Equal(t, "mysql.query", spans[0].OperationName())
		assert.Equal(t, "mysql.query", spans[1].OperationName())
	},
}

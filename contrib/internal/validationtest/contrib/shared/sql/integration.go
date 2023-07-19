// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package sqltest // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/sqltest"

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

type Integration struct {
	NumSpans int
}

// Prepare sets up a table with the given name in both the MySQL and Postgres databases and returns
// a teardown function which will drop it.
func Prepare(t *testing.T, tableName string) func() {
	queryDrop := fmt.Sprintf("DROP TABLE IF EXISTS %s", tableName)
	queryCreate := fmt.Sprintf("CREATE TABLE %s (id integer NOT NULL DEFAULT '0', name text)", tableName)

	mysql, err := sql.Open("mysql", "test:test@tcp(127.0.0.1:3306)/test")
	require.NoError(t, err)
	mysql.Exec(queryDrop)
	mysql.Exec(queryCreate)

	postgres, err := sql.Open("postgres", "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable")
	require.NoError(t, err)
	postgres.Exec(queryDrop)
	postgres.Exec(queryCreate)

	// mssql, err := sql.Open("sqlserver", "sqlserver://sa:myPassw0rd@localhost:1433?database=master")
	// require.NoError(t, err)
	// mssql.Exec(queryDrop)
	// mssql.Exec(queryCreate)

	return func() {
		mysql.Exec(queryDrop)
		postgres.Exec(queryDrop)
		//mssql.Exec(queryDrop)
		mysql.Close()
		postgres.Close()
		// mssql.Close()
	}
}

// RunAll applies a sequence of unit tests to check the correct tracing of sql features.
func RunAll(t *testing.T, cfg *Config) int {

	cfg.DB.SetMaxIdleConns(0)

	for name, test := range map[string]func(*Config) func(*testing.T){
		"Connect":       testConnect,
		"Ping":          testPing,
		"Query":         testQuery,
		"Statement":     testStatement,
		"BeginRollback": testBeginRollback,
		"Exec":          testExec,
	} {
		t.Run(name, test(cfg))
	}
	var sum int
	for _, val := range cfg.OperationToNumSpans {
		sum += val
	}
	return sum
}

func testConnect(cfg *Config) func(*testing.T) {
	return func(t *testing.T) {
		err := cfg.DB.Ping()
		require.NoError(t, err)
	}
}

func testPing(cfg *Config) func(*testing.T) {
	return func(t *testing.T) {
		err := cfg.DB.Ping()
		require.NoError(t, err)
	}
}

func testQuery(cfg *Config) func(*testing.T) {
	var query string
	switch cfg.DriverName {
	case "postgres", "pgx", "mysql":
		query = fmt.Sprintf("SELECT id, name FROM %s LIMIT 5", cfg.TableName)
	case "sqlserver":
		query = fmt.Sprintf("SELECT TOP 5 id, name FROM %s", cfg.TableName)
	}
	return func(t *testing.T) {
		rows, err := cfg.DB.Query(query)
		defer rows.Close()
		require.NoError(t, err)

		if cfg.DriverName == "sqlserver" {
			//The mssql driver doesn't support non-prepared queries so there are 3 spans
			//connect, prepare, and query
			cfg.OperationToNumSpans["Query"] += 3
		}
	}
}

func testStatement(cfg *Config) func(*testing.T) {
	query := "INSERT INTO %s(name) VALUES(%s)"
	switch cfg.DriverName {
	case "postgres", "pgx":
		query = fmt.Sprintf(query, cfg.TableName, "$1")
	case "mysql":
		query = fmt.Sprintf(query, cfg.TableName, "?")
	case "sqlserver":
		query = fmt.Sprintf(query, cfg.TableName, "@p1")
	}
	return func(t *testing.T) {
		stmt, err := cfg.DB.Prepare(query)
		require.NoError(t, err)

		_, err2 := stmt.Exec("New York")
		require.NoError(t, err2)
	}
}

func testBeginRollback(cfg *Config) func(*testing.T) {
	return func(t *testing.T) {
		tx, err := cfg.DB.Begin()
		require.NoError(t, err)

		err = tx.Rollback()
		require.NoError(t, err)
	}
}

func testExec(cfg *Config) func(*testing.T) {
	return func(t *testing.T) {
		query := fmt.Sprintf("INSERT INTO %s(name) VALUES('New York')", cfg.TableName)

		parent, ctx := tracer.StartSpanFromContext(context.Background(), "test.parent",
			tracer.ServiceName("test"),
			tracer.ResourceName("parent"),
		)

		tx, err := cfg.DB.BeginTx(ctx, nil)
		require.NoError(t, err)

		_, err = tx.ExecContext(ctx, query)
		require.NoError(t, err)
		err = tx.Commit()
		require.NoError(t, err)

		parent.Finish() // flush children

		if cfg.DriverName == "sqlserver" {
			//The mssql driver doesn't support non-prepared exec so there are 2 extra spans for the exec:
			//prepare, exec, and then a close
			cfg.OperationToNumSpans["Exec"] += 2
		}
	}
}

// Config holds the test configuration.
type Config struct {
	*sql.DB
	OperationToNumSpans map[string]int
	DriverName          string
	TableName           string
}

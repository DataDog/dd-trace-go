// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sqltest // import "gopkg.in/CodapeWild/dd-trace-go.v1/contrib/internal/sqltest"

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"testing"

	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace/tracer"

	"github.com/stretchr/testify/assert"
)

// Prepare sets up a table with the given name in both the MySQL and Postgres databases and returns
// a teardown function which will drop it.
func Prepare(tableName string) func() {
	queryDrop := fmt.Sprintf("DROP TABLE IF EXISTS %s", tableName)
	queryCreate := fmt.Sprintf("CREATE TABLE %s (id integer NOT NULL DEFAULT '0', name text)", tableName)
	mysql, err := sql.Open("mysql", "test:test@tcp(127.0.0.1:3306)/test")
	defer mysql.Close()
	if err != nil {
		log.Fatal(err)
	}
	mysql.Exec(queryDrop)
	mysql.Exec(queryCreate)
	postgres, err := sql.Open("postgres", "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable")
	defer postgres.Close()
	if err != nil {
		log.Fatal(err)
	}
	postgres.Exec(queryDrop)
	postgres.Exec(queryCreate)
	return func() {
		mysql.Exec(queryDrop)
		postgres.Exec(queryDrop)
	}
}

// RunAll applies a sequence of unit tests to check the correct tracing of sql features.
func RunAll(t *testing.T, cfg *Config) {
	cfg.mockTracer = mocktracer.Start()
	defer cfg.mockTracer.Stop()

	for name, test := range map[string]func(*Config) func(*testing.T){
		"Ping":          testPing,
		"Query":         testQuery,
		"Statement":     testStatement,
		"BeginRollback": testBeginRollback,
		"Exec":          testExec,
	} {
		t.Run(name, test(cfg))
	}
}

func testPing(cfg *Config) func(*testing.T) {
	return func(t *testing.T) {
		cfg.mockTracer.Reset()
		assert := assert.New(t)
		err := cfg.DB.Ping()
		assert.Nil(err)
		spans := cfg.mockTracer.FinishedSpans()
		assert.Len(spans, 1)

		span := spans[0]
		assert.Equal(cfg.ExpectName, span.OperationName())
		cfg.ExpectTags["sql.query_type"] = "Ping"
		for k, v := range cfg.ExpectTags {
			assert.Equal(v, span.Tag(k), "Value mismatch on tag %s", k)
		}
	}
}

func testQuery(cfg *Config) func(*testing.T) {
	query := fmt.Sprintf("SELECT id, name FROM %s LIMIT 5", cfg.TableName)
	return func(t *testing.T) {
		cfg.mockTracer.Reset()
		assert := assert.New(t)
		rows, err := cfg.DB.Query(query)
		defer rows.Close()
		assert.Nil(err)

		spans := cfg.mockTracer.FinishedSpans()
		assert.Len(spans, 1)

		span := spans[0]
		cfg.ExpectTags["sql.query_type"] = "Query"
		assert.Equal(cfg.ExpectName, span.OperationName())
		for k, v := range cfg.ExpectTags {
			assert.Equal(v, span.Tag(k), "Value mismatch on tag %s", k)
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
	}
	return func(t *testing.T) {
		cfg.mockTracer.Reset()
		assert := assert.New(t)
		stmt, err := cfg.DB.Prepare(query)
		assert.Equal(nil, err)

		spans := cfg.mockTracer.FinishedSpans()
		assert.Len(spans, 1)

		span := spans[0]
		assert.Equal(cfg.ExpectName, span.OperationName())
		cfg.ExpectTags["sql.query_type"] = "Prepare"
		for k, v := range cfg.ExpectTags {
			assert.Equal(v, span.Tag(k), "Value mismatch on tag %s", k)
		}

		cfg.mockTracer.Reset()
		_, err2 := stmt.Exec("New York")
		assert.Equal(nil, err2)

		spans = cfg.mockTracer.FinishedSpans()
		assert.Len(spans, 1)
		span = spans[0]
		assert.Equal(cfg.ExpectName, span.OperationName())
		cfg.ExpectTags["sql.query_type"] = "Exec"
		for k, v := range cfg.ExpectTags {
			assert.Equal(v, span.Tag(k), "Value mismatch on tag %s", k)
		}
	}
}

func testBeginRollback(cfg *Config) func(*testing.T) {
	return func(t *testing.T) {
		cfg.mockTracer.Reset()
		assert := assert.New(t)

		tx, err := cfg.DB.Begin()
		assert.Equal(nil, err)

		spans := cfg.mockTracer.FinishedSpans()
		assert.Len(spans, 1)

		span := spans[0]
		assert.Equal(cfg.ExpectName, span.OperationName())
		cfg.ExpectTags["sql.query_type"] = "Begin"
		for k, v := range cfg.ExpectTags {
			assert.Equal(v, span.Tag(k), "Value mismatch on tag %s", k)
		}

		cfg.mockTracer.Reset()
		err = tx.Rollback()
		assert.Equal(nil, err)

		spans = cfg.mockTracer.FinishedSpans()
		assert.Len(spans, 1)
		span = spans[0]
		assert.Equal(cfg.ExpectName, span.OperationName())
		cfg.ExpectTags["sql.query_type"] = "Rollback"
		for k, v := range cfg.ExpectTags {
			assert.Equal(v, span.Tag(k), "Value mismatch on tag %s", k)
		}
	}
}

func testExec(cfg *Config) func(*testing.T) {
	return func(t *testing.T) {
		assert := assert.New(t)
		query := fmt.Sprintf("INSERT INTO %s(name) VALUES('New York')", cfg.TableName)

		parent, ctx := tracer.StartSpanFromContext(context.Background(), "test.parent",
			tracer.ServiceName("test"),
			tracer.ResourceName("parent"),
		)

		cfg.mockTracer.Reset()
		tx, err := cfg.DB.BeginTx(ctx, nil)
		assert.Equal(nil, err)
		_, err = tx.ExecContext(ctx, query)
		assert.Equal(nil, err)
		err = tx.Commit()
		assert.Equal(nil, err)

		parent.Finish() // flush children

		spans := cfg.mockTracer.FinishedSpans()
		assert.Len(spans, 4)

		var span mocktracer.Span
		for _, s := range spans {
			if s.OperationName() == cfg.ExpectName && s.Tag(ext.ResourceName) == query {
				span = s
			}
		}
		assert.NotNil(span, "span not found")
		cfg.ExpectTags["sql.query_type"] = "Exec"
		for k, v := range cfg.ExpectTags {
			assert.Equal(v, span.Tag(k), "Value mismatch on tag %s", k)
		}
		for _, s := range spans {
			if s.OperationName() == cfg.ExpectName && s.Tag(ext.ResourceName) == "Commit" {
				span = s
			}
		}
		assert.NotNil(span, "span not found")
		cfg.ExpectTags["sql.query_type"] = "Commit"
		for k, v := range cfg.ExpectTags {
			assert.Equal(v, span.Tag(k), "Value mismatch on tag %s", k)
		}
	}
}

// Config holds the test configuration.
type Config struct {
	*sql.DB
	mockTracer mocktracer.Tracer
	DriverName string
	TableName  string
	ExpectName string
	ExpectTags map[string]interface{}
}

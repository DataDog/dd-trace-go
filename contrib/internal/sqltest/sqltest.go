package sqltest

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"testing"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/tracertest"

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
	expectedSpan := cfg.Expected
	return func(t *testing.T) {
		assert := assert.New(t)
		err := cfg.DB.Ping()
		assert.Nil(err)
		spans := traceSpans(assert, cfg)
		assert.Len(spans, 1)

		span := spans[0]
		pingSpan := tracertest.CopySpan(expectedSpan, cfg.Tracer)
		pingSpan.Resource = "Ping"
		tracertest.CompareSpan(t, pingSpan, span)
	}
}

func testQuery(cfg *Config) func(*testing.T) {
	query := fmt.Sprintf("SELECT id, name FROM %s LIMIT 5", cfg.TableName)
	expectedSpan := cfg.Expected
	return func(t *testing.T) {
		assert := assert.New(t)
		rows, err := cfg.DB.Query(query)
		defer rows.Close()
		assert.Nil(err)

		spans := traceSpans(assert, cfg)
		assert.Len(spans, 1)

		span := spans[0]
		querySpan := tracertest.CopySpan(expectedSpan, cfg.Tracer)
		querySpan.Resource = query
		querySpan.SetMeta("sql.query", query)
		tracertest.CompareSpan(t, querySpan, span)
		delete(expectedSpan.Meta, "sql.query")
	}
}

func testStatement(cfg *Config) func(*testing.T) {
	query := "INSERT INTO %s(name) VALUES(%s)"
	expectedSpan := cfg.Expected
	switch cfg.DriverName {
	case "postgres":
		query = fmt.Sprintf(query, cfg.TableName, "$1")
	case "mysql":
		query = fmt.Sprintf(query, cfg.TableName, "?")
	}
	return func(t *testing.T) {
		assert := assert.New(t)
		stmt, err := cfg.DB.Prepare(query)
		assert.Equal(nil, err)

		spans := traceSpans(assert, cfg)
		assert.Len(spans, 1)

		actualSpan := spans[0]
		prepareSpan := tracertest.CopySpan(expectedSpan, cfg.Tracer)
		prepareSpan.Resource = query
		prepareSpan.SetMeta("sql.query", query)
		tracertest.CompareSpan(t, prepareSpan, actualSpan)
		delete(expectedSpan.Meta, "sql.query")

		_, err2 := stmt.Exec("New York")
		assert.Equal(nil, err2)

		spans = traceSpans(assert, cfg)
		assert.Len(spans, 1)
		actualSpan = spans[0]

		execSpan := tracertest.CopySpan(expectedSpan, cfg.Tracer)
		execSpan.Resource = query
		execSpan.SetMeta("sql.query", query)
		tracertest.CompareSpan(t, execSpan, actualSpan)
		delete(expectedSpan.Meta, "sql.query")
	}
}

func testBeginRollback(cfg *Config) func(*testing.T) {
	expectedSpan := cfg.Expected
	return func(t *testing.T) {
		assert := assert.New(t)

		tx, err := cfg.DB.Begin()
		assert.Equal(nil, err)

		spans := traceSpans(assert, cfg)
		assert.Len(spans, 1)

		actualSpan := spans[0]
		beginSpan := tracertest.CopySpan(expectedSpan, cfg.Tracer)
		beginSpan.Resource = "Begin"
		tracertest.CompareSpan(t, beginSpan, actualSpan)

		err = tx.Rollback()
		assert.Equal(nil, err)

		spans = traceSpans(assert, cfg)
		assert.Len(spans, 1)
		actualSpan = spans[0]
		rollbackSpan := tracertest.CopySpan(expectedSpan, cfg.Tracer)
		rollbackSpan.Resource = "Rollback"
		tracertest.CompareSpan(t, rollbackSpan, actualSpan)
	}
}

func testExec(cfg *Config) func(*testing.T) {
	expectedSpan := cfg.Expected
	return func(t *testing.T) {
		assert := assert.New(t)
		query := fmt.Sprintf("INSERT INTO %s(name) VALUES('New York')", cfg.TableName)

		parent := cfg.Tracer.NewRootSpan("test.parent", "test", "parent")
		ctx := tracer.ContextWithSpan(context.Background(), parent)

		tx, err := cfg.DB.BeginTx(ctx, nil)
		assert.Equal(nil, err)
		_, err = tx.ExecContext(ctx, query)
		assert.Equal(nil, err)
		err = tx.Commit()
		assert.Equal(nil, err)

		parent.Finish() // flush children

		spans := traceSpans(assert, cfg)
		assert.Len(spans, 4)

		span := new(tracer.Span)
		for _, s := range spans {
			if s.Name == expectedSpan.Name && s.Resource == query {
				span = s
			}
		}

		assert.NotNil(span)
		execSpan := tracertest.CopySpan(expectedSpan, cfg.Tracer)
		execSpan.Resource = query
		execSpan.SetMeta("sql.query", query)
		tracertest.CompareSpan(t, execSpan, span)
		delete(expectedSpan.Meta, "sql.query")

		for _, s := range spans {
			if s.Name == expectedSpan.Name && s.Resource == "Commit" {
				span = s
			}
		}

		assert.NotNil(span)
		commitSpan := tracertest.CopySpan(expectedSpan, cfg.Tracer)
		commitSpan.Resource = "Commit"
		tracertest.CompareSpan(t, commitSpan, span)
	}
}

// traceSpans flushes and returns the spans held by the tracer in the config. It expects the tracer
// to have created exactly one trace.
func traceSpans(assert *assert.Assertions, cfg *Config) []*tracer.Span {
	cfg.Tracer.ForceFlush()
	traces := cfg.Transport.Traces()
	assert.Len(traces, 1)
	return traces[0]
}

// Config holds the test configuration.
type Config struct {
	*sql.DB
	Tracer     *tracer.Tracer
	Transport  *tracertest.DummyTransport
	DriverName string
	TableName  string
	Expected   *tracer.Span
}

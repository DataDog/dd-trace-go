package sqltest

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"testing"

	"github.com/DataDog/dd-trace-go/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/ddtrace/tracer"

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
		for k, v := range cfg.ExpectTags {
			assert.Equal(v, span.Tag(k), "tag mismatch")
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
		assert.Equal(cfg.ExpectName, span.OperationName())
		for k, v := range cfg.ExpectTags {
			assert.Equal(v, span.Tag(k), "tag mismatch")
		}
	}
}

func testStatement(cfg *Config) func(*testing.T) {
	query := "INSERT INTO %s(name) VALUES(%s)"
	switch cfg.DriverName {
	case "postgres":
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
		for k, v := range cfg.ExpectTags {
			assert.Equal(v, span.Tag(k), "tag mismatch")
		}

		cfg.mockTracer.Reset()
		_, err2 := stmt.Exec("New York")
		assert.Equal(nil, err2)

		spans = cfg.mockTracer.FinishedSpans()
		assert.Len(spans, 1)
		span = spans[0]
		assert.Equal(cfg.ExpectName, span.OperationName())
		for k, v := range cfg.ExpectTags {
			assert.Equal(v, span.Tag(k), "tag mismatch")
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
		for k, v := range cfg.ExpectTags {
			assert.Equal(v, span.Tag(k), "tag mismatch")
		}

		cfg.mockTracer.Reset()
		err = tx.Rollback()
		assert.Equal(nil, err)

		spans = cfg.mockTracer.FinishedSpans()
		assert.Len(spans, 1)
		span = spans[0]
		assert.Equal(cfg.ExpectName, span.OperationName())
		for k, v := range cfg.ExpectTags {
			assert.Equal(v, span.Tag(k), "tag mismatch")
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
		for k, v := range cfg.ExpectTags {
			assert.Equal(v, span.Tag(k), "tag mismatch")
		}
		for _, s := range spans {
			if s.OperationName() == cfg.ExpectName && s.Tag(ext.ResourceName) == "Commit" {
				span = s
			}
		}
		assert.NotNil(span, "span not found")
		for k, v := range cfg.ExpectTags {
			assert.Equal(v, span.Tag(k), "tag mismatch")
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

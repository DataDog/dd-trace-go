package sqlutils

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/tracertest"
	"github.com/stretchr/testify/assert"
)

// AllSQLTests applies a sequence of unit tests to check the correct tracing of sql features.
func AllSQLTests(t *testing.T, db *DB, expectedSpan *tracer.Span) {
	testDB(t, db, expectedSpan)
	testStatement(t, db, expectedSpan)
	testTransaction(t, db, expectedSpan)
}

func testDB(t *testing.T, db *DB, expectedSpan *tracer.Span) {
	assert := assert.New(t)
	const query = "select id, name, population from city limit 5"

	// Test db.Ping
	err := db.Ping()
	assert.Equal(nil, err)

	db.Tracer.FlushTraces()
	traces := db.Transport.Traces()
	assert.Len(traces, 1)
	spans := traces[0]
	assert.Len(spans, 1)

	actualSpan := spans[0]
	pingSpan := tracertest.CopySpan(expectedSpan, db.Tracer)
	pingSpan.Resource = "Ping"
	tracertest.CompareSpan(t, pingSpan, actualSpan)

	// Test db.Query
	rows, err := db.Query(query)
	defer rows.Close()
	assert.Equal(nil, err)

	db.Tracer.FlushTraces()
	traces = db.Transport.Traces()
	assert.Len(traces, 1)
	spans = traces[0]
	assert.Len(spans, 1)

	actualSpan = spans[0]
	querySpan := tracertest.CopySpan(expectedSpan, db.Tracer)
	querySpan.Resource = query
	querySpan.SetMeta("sql.query", query)
	tracertest.CompareSpan(t, querySpan, actualSpan)
	delete(expectedSpan.Meta, "sql.query")
}

func testStatement(t *testing.T, db *DB, expectedSpan *tracer.Span) {
	assert := assert.New(t)
	query := "INSERT INTO city(name) VALUES(%s)"
	switch db.DriverName {
	case "postgres":
		query = fmt.Sprintf(query, "$1")
	case "mysql":
		query = fmt.Sprintf(query, "?")
	}

	// Test TracedConn.PrepareContext
	stmt, err := db.Prepare(query)
	assert.Equal(nil, err)

	db.Tracer.FlushTraces()
	traces := db.Transport.Traces()
	assert.Len(traces, 1)
	spans := traces[0]
	assert.Len(spans, 1)

	actualSpan := spans[0]
	prepareSpan := tracertest.CopySpan(expectedSpan, db.Tracer)
	prepareSpan.Resource = query
	prepareSpan.SetMeta("sql.query", query)
	tracertest.CompareSpan(t, prepareSpan, actualSpan)
	delete(expectedSpan.Meta, "sql.query")

	// Test Exec
	_, err2 := stmt.Exec("New York")
	assert.Equal(nil, err2)

	db.Tracer.FlushTraces()
	traces = db.Transport.Traces()
	assert.Len(traces, 1)
	spans = traces[0]
	assert.Len(spans, 1)
	actualSpan = spans[0]

	execSpan := tracertest.CopySpan(expectedSpan, db.Tracer)
	execSpan.Resource = query
	execSpan.SetMeta("sql.query", query)
	tracertest.CompareSpan(t, execSpan, actualSpan)
	delete(expectedSpan.Meta, "sql.query")
}

func testTransaction(t *testing.T, db *DB, expectedSpan *tracer.Span) {
	assert := assert.New(t)
	query := "INSERT INTO city(name) VALUES('New York')"

	// Test Begin
	tx, err := db.Begin()
	assert.Equal(nil, err)

	db.Tracer.FlushTraces()
	traces := db.Transport.Traces()
	assert.Len(traces, 1)
	spans := traces[0]
	assert.Len(spans, 1)

	actualSpan := spans[0]
	beginSpan := tracertest.CopySpan(expectedSpan, db.Tracer)
	beginSpan.Resource = "Begin"
	tracertest.CompareSpan(t, beginSpan, actualSpan)

	// Test Rollback
	err = tx.Rollback()
	assert.Equal(nil, err)

	db.Tracer.FlushTraces()
	traces = db.Transport.Traces()
	assert.Len(traces, 1)
	spans = traces[0]
	assert.Len(spans, 1)
	actualSpan = spans[0]
	rollbackSpan := tracertest.CopySpan(expectedSpan, db.Tracer)
	rollbackSpan.Resource = "Rollback"
	tracertest.CompareSpan(t, rollbackSpan, actualSpan)

	// Test Exec
	parentSpan := db.Tracer.NewRootSpan("test.parent", "test", "parent")
	ctx := tracer.ContextWithSpan(context.Background(), parentSpan)

	tx, err = db.BeginTx(ctx, nil)
	assert.Equal(nil, err)

	_, err = tx.ExecContext(ctx, query)
	assert.Equal(nil, err)

	err = tx.Commit()
	assert.Equal(nil, err)

	db.Tracer.FlushTraces()
	traces = db.Transport.Traces()
	assert.Len(traces, 1)
	spans = traces[0]
	assert.Len(spans, 3)

	actualSpan = spans[1]
	execSpan := tracertest.CopySpan(expectedSpan, db.Tracer)
	execSpan.Resource = query
	execSpan.SetMeta("sql.query", query)
	tracertest.CompareSpan(t, execSpan, actualSpan)
	delete(expectedSpan.Meta, "sql.query")

	actualSpan = spans[2]
	commitSpan := tracertest.CopySpan(expectedSpan, db.Tracer)
	commitSpan.Resource = "Commit"
	tracertest.CompareSpan(t, commitSpan, actualSpan)
}

// DB is a struct dedicated for testing
type DB struct {
	*sql.DB
	Tracer     *tracer.Tracer
	Transport  *tracertest.DummyTransport
	DriverName string
}

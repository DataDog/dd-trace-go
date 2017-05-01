package sqltraced

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"log"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/ext"
	"github.com/stretchr/testify/assert"
)

const DEBUG = true

// Complete sequence of tests to run for each driver
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
	pingSpan := copySpan(expectedSpan, db.Tracer)
	pingSpan.Resource = "Ping"
	compareSpan(t, pingSpan, actualSpan)

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
	querySpan := copySpan(expectedSpan, db.Tracer)
	querySpan.Resource = query
	querySpan.SetMeta("sql.query", query)
	compareSpan(t, querySpan, actualSpan)
	delete(expectedSpan.Meta, "sql.query")
}

func testStatement(t *testing.T, db *DB, expectedSpan *tracer.Span) {
	assert := assert.New(t)
	query := "INSERT INTO city(name) VALUES(%s)"
	switch strings.ToLower(db.Name) {
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
	prepareSpan := copySpan(expectedSpan, db.Tracer)
	prepareSpan.Resource = query
	prepareSpan.SetMeta("sql.query", query)
	compareSpan(t, prepareSpan, actualSpan)
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

	execSpan := copySpan(expectedSpan, db.Tracer)
	execSpan.Resource = query
	execSpan.SetMeta("sql.query", query)
	compareSpan(t, execSpan, actualSpan)
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
	beginSpan := copySpan(expectedSpan, db.Tracer)
	beginSpan.Resource = "Begin"
	compareSpan(t, beginSpan, actualSpan)

	// Test Rollback
	err = tx.Rollback()
	assert.Equal(nil, err)

	db.Tracer.FlushTraces()
	traces = db.Transport.Traces()
	assert.Len(traces, 1)
	spans = traces[0]
	assert.Len(spans, 1)
	actualSpan = spans[0]
	rollbackSpan := copySpan(expectedSpan, db.Tracer)
	rollbackSpan.Resource = "Rollback"
	compareSpan(t, rollbackSpan, actualSpan)

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
	execSpan := copySpan(expectedSpan, db.Tracer)
	execSpan.Resource = query
	execSpan.SetMeta("sql.query", query)
	compareSpan(t, execSpan, actualSpan)
	delete(expectedSpan.Meta, "sql.query")

	actualSpan = spans[2]
	commitSpan := copySpan(expectedSpan, db.Tracer)
	commitSpan.Resource = "Commit"
	compareSpan(t, commitSpan, actualSpan)
}

type DB struct {
	*sql.DB
	Name      string
	Service   string
	Tracer    *tracer.Tracer
	Transport *DummyTransport
}

func NewDB(name, service string, driver driver.Driver, dsn string) *DB {
	tracer, transport := GetTestTracer()
	tracer.DebugLoggingEnabled = DEBUG
	Register(name, service, driver, tracer)
	db, err := sql.Open(name, dsn)
	if err != nil {
		log.Fatal(err)
	}

	return &DB{
		db,
		name,
		service,
		tracer,
		transport,
	}
}

func copySpan(span *tracer.Span, trc *tracer.Tracer) *tracer.Span {
	newSpan := tracer.NewSpan(span.Name, span.Service, span.Resource, span.SpanID, span.TraceID, span.ParentID, trc)
	newSpan.Type = ext.SQLType
	newSpan.Meta = span.Meta
	return newSpan
}

// Test all fields of the span
func compareSpan(t *testing.T, expectedSpan, actualSpan *tracer.Span) {
	assert := assert.New(t)
	if DEBUG {
		fmt.Printf("-> ExpectedSpan: \n%s\n\n", expectedSpan)
	}
	assert.Equal(expectedSpan.Name, actualSpan.Name)
	assert.Equal(expectedSpan.Service, actualSpan.Service)
	assert.Equal(expectedSpan.Resource, actualSpan.Resource)
	assert.Equal(expectedSpan.Type, actualSpan.Type)
	assert.True(reflect.DeepEqual(expectedSpan.Meta, actualSpan.Meta))
}

// Return a Tracer with a DummyTransport
func GetTestTracer() (*tracer.Tracer, *DummyTransport) {
	transport := &DummyTransport{}
	tracer := tracer.NewTracerTransport(transport)
	return tracer, transport
}

// dummyTransport is a transport that just buffers spans and encoding
type DummyTransport struct {
	traces   [][]*tracer.Span
	services map[string]tracer.Service
}

func (t *DummyTransport) SendTraces(traces [][]*tracer.Span) (*http.Response, error) {
	t.traces = append(t.traces, traces...)
	return nil, nil
}

func (t *DummyTransport) SendServices(services map[string]tracer.Service) (*http.Response, error) {
	t.services = services
	return nil, nil
}

func (t *DummyTransport) Traces() [][]*tracer.Span {
	traces := t.traces
	t.traces = nil
	return traces
}

func (t *DummyTransport) SetHeader(key, value string) {}

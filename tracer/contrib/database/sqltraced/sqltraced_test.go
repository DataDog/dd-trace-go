package sqltraced

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"log"
	"net/http"
	"reflect"
	"testing"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/contrib"
	"github.com/stretchr/testify/assert"
)

const DEBUG = true

// Complete sequence of tests to run for each driver
func AllTests(t *testing.T, db *DB, expectedSpan tracer.Span) {
	testPing(t, db, expectedSpan)
	testConnectionQuery(t, db, expectedSpan)
	testStatement(t, db, expectedSpan)
	testTransaction(t, db, expectedSpan)
}

func testPing(t *testing.T, db *DB, expectedSpan tracer.Span) {
	assert := assert.New(t)

	err := db.Ping()
	assert.Equal(nil, err)

	db.Tracer.FlushTraces()
	traces := db.Transport.Traces()
	assert.Len(traces, 1)
	spans := traces[0]
	assert.Len(spans, 1)

	actualSpan := spans[0]
	expectedSpan.Name += "ping"
	expectedSpan.Resource = expectedSpan.Name
	compareSpan(assert, &expectedSpan, actualSpan)
}

func testConnectionQuery(t *testing.T, db *DB, expectedSpan tracer.Span) {
	assert := assert.New(t)

	const query = "select id, name, population from city limit 5"
	rows, err := db.Query(query)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	db.Tracer.FlushTraces()
	traces := db.Transport.Traces()
	assert.Len(traces, 1)
	spans := traces[0]
	assert.Len(spans, 1)

	actualSpan := spans[0]
	expectedSpan.Name += "query"
	expectedSpan.Resource = query
	expectedSpan.SetMeta("sql.query", query)
	compareSpan(assert, &expectedSpan, actualSpan)
	delete(expectedSpan.Meta, "sql.query")
}

func testStatement(t *testing.T, db *DB, expectedSpan tracer.Span) {
	assert := assert.New(t)
	query := "INSERT INTO city(name) VALUES(%s)"
	if db.Name == "Postgres" {
		query = fmt.Sprintf(query, "$1")
	} else {
		query = fmt.Sprintf(query, "?")
	}

	// Test TracedConn.PrepareContext
	_, err := db.Prepare(query)
	assert.Equal(nil, err)

	db.Tracer.FlushTraces()
	traces := db.Transport.Traces()
	assert.Len(traces, 1)
	spans := traces[0]
	assert.Len(spans, 1)
	actualSpan := spans[0]
	prepareSpan := expectedSpan
	prepareSpan.Name += "prepare"
	prepareSpan.Resource = query
	prepareSpan.SetMeta("sql.query", query)
	compareSpan(assert, &prepareSpan, actualSpan)
	delete(expectedSpan.Meta, "sql.query")

	// Test Exec
	//_, err2 := stmt.Exec("New York")
	//assert.Equal(nil, err2)

	//db.Tracer.FlushTraces()
	//traces = db.Transport.Traces()
	//assert.Len(traces, 1)
	//spans = traces[0]
	//assert.Len(spans, 1)
	//actualSpan = spans[0]

	//execSpan := expectedSpan
	//execSpan.Name += "exec"
	//execSpan.Resource = query
	//execSpan.SetMeta("sql.query", query)
	//compareSpan(assert, &execSpan, actualSpan)
	//delete(expectedSpan.Meta, "sql.query")

	//lastId, err3 := res.LastInsertId()
	//if db.Name != "Postgres" {
	//	assert.Equal(nil, err3)
	//	assert.Equal(0, lastId)
	//}

	//rowCnt, err4 := res.RowsAffected()
	//assert.Equal(nil, err4)
	//assert.NotEqual(0, rowCnt)
	//actualSpan = spans[0]
	//expectedSpan.Name += "prepare"
	//expectedSpan.Resource = query
	//expectedSpan.SetMeta("sql.query", query)
	//compareSpan(assert, &expectedSpan, actualSpan)
	//delete(expectedSpan.Meta, "sql.query")
}

func testTransaction(t *testing.T, db *DB, expectedSpan tracer.Span) {
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
	beginSpan := expectedSpan
	beginSpan.Name += "begin"
	beginSpan.Resource = "Begin"
	compareSpan(assert, &beginSpan, actualSpan)

	// Test Rollback
	err = tx.Rollback()
	assert.Equal(nil, err)

	db.Tracer.FlushTraces()
	traces = db.Transport.Traces()
	assert.Len(traces, 1)
	spans = traces[0]
	assert.Len(spans, 1)
	actualSpan = spans[0]
	rollbackSpan := expectedSpan
	rollbackSpan.Name += "rollback"
	rollbackSpan.Resource = "Rollback"
	compareSpan(assert, &rollbackSpan, actualSpan)

	// Test Exec
	tx, err = db.Begin()
	assert.Equal(nil, err)
	_, err = tx.Exec(query)
	assert.Equal(nil, err)

	db.Tracer.FlushTraces()
	traces = db.Transport.Traces()
	assert.Len(traces, 2)
	spans = traces[1]
	assert.Len(spans, 1)
	actualSpan = spans[0]
	execSpan := expectedSpan
	execSpan.Name += "exec"
	execSpan.Resource = query
	execSpan.SetMeta("sql.query", query)
	compareSpan(assert, &execSpan, actualSpan)
	delete(expectedSpan.Meta, "sql.query")

	// Test Commit
	err = tx.Commit()
	assert.Equal(nil, err)

	db.Tracer.FlushTraces()
	traces = db.Transport.Traces()
	assert.Len(traces, 1)
	spans = traces[0]
	assert.Len(spans, 1)
	actualSpan = spans[0]
	commitSpan := expectedSpan
	commitSpan.Name += "commit"
	commitSpan.Resource = "Commit"
	compareSpan(assert, &commitSpan, actualSpan)
}

type DB struct {
	*sql.DB
	contrib.Config
	Name      string
	Service   string
	Tracer    *tracer.Tracer
	Transport *dummyTransport
}

func NewDB(name, service string, driver driver.Driver, config contrib.Config) *DB {
	tracer, transport := getTestTracer()
	tracer.DebugLoggingEnabled = DEBUG
	Register(name, service, driver, tracer)
	db, err := sql.Open(name, config.DSN())
	if err != nil {
		log.Fatal(err)
	}

	return &DB{
		db,
		config,
		name,
		service,
		tracer,
		transport,
	}
}

// Test all fields of the span
func compareSpan(assert *assert.Assertions, expectedSpan, actualSpan *tracer.Span) {
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
func getTestTracer() (*tracer.Tracer, *dummyTransport) {
	transport := &dummyTransport{}
	tracer := tracer.NewTracerTransport(transport)
	return tracer, transport
}

// dummyTransport is a transport that just buffers spans and encoding
type dummyTransport struct {
	traces   [][]*tracer.Span
	services map[string]tracer.Service
}

func (t *dummyTransport) SendTraces(traces [][]*tracer.Span) (*http.Response, error) {
	t.traces = append(t.traces, traces...)
	return nil, nil
}

func (t *dummyTransport) SendServices(services map[string]tracer.Service) (*http.Response, error) {
	t.services = services
	return nil, nil
}

func (t *dummyTransport) Traces() [][]*tracer.Span {
	traces := t.traces
	t.traces = nil
	return traces
}

func (t *dummyTransport) SetHeader(key, value string) {}

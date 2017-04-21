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
	compareSpan(t, &expectedSpan, actualSpan)
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
	compareSpan(t, &expectedSpan, actualSpan)
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
func compareSpan(t *testing.T, expectedSpan, actualSpan *tracer.Span) {
	if DEBUG {
		fmt.Printf("-> ExpectedSpan: \n%s\n\n", expectedSpan)
	}
	assert := assert.New(t)
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

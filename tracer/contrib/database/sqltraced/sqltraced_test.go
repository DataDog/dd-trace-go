package sqltraced

import (
	"database/sql"
	"database/sql/driver"
	"log"
	"net/http"
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/contrib"
	"github.com/stretchr/testify/assert"
)

const DEBUG = true

// Complete sequence of tests to run for each driver
func AllTests(t *testing.T, db *DB) {
	testPing(t, db)
	testConnectionQuery(t, db)
}

func testPing(t *testing.T, db *DB) {
	assert := assert.New(t)

	err := db.Ping()

	db.Tracer.FlushTraces()
	traces := db.Transport.Traces()
	assert.Len(traces, 1)
	spans := traces[0]
	assert.Len(spans, 1)
	actualSpan := spans[0]
	println(actualSpan.String())

	assert.Equal(err, nil)
}

func testConnectionQuery(t *testing.T, db *DB) {
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

	expectedSpan := &tracer.Span{
		Name:     strings.ToLower(db.Name) + ".query",
		Service:  db.Service,
		Resource: query,
	}
	expectedSpan.SetMeta("sql.query", query)
	expectedSpan.SetMeta("args", "[]")
	expectedSpan.SetMeta("args_length", "0")
	compareSpan(t, expectedSpan, actualSpan)
}

type DB struct {
	*sql.DB
	Name      string
	Service   string
	Tracer    *tracer.Tracer
	Transport *dummyTransport
}

func NewDB(name, service string, driver driver.Driver, config contrib.Config) *DB {
	tracer, transport := getTestTracer()
	tracer.DebugLoggingEnabled = DEBUG
	Register(name, service, driver, tracer)
	db, err := sql.Open(name, config.Format())
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

// Test all fields of the span
func compareSpan(t *testing.T, expectedSpan, actualSpan *tracer.Span) {
	assert := assert.New(t)
	assert.Equal(expectedSpan.Name, actualSpan.Name)
	assert.Equal(expectedSpan.Service, actualSpan.Service)
	assert.Equal(expectedSpan.Resource, actualSpan.Resource)
	assert.Equal(expectedSpan.GetMeta("sql.query"), actualSpan.GetMeta("sql.query"))
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

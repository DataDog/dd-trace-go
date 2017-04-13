package sqltraced

import (
	"database/sql"
	"log"
	"net/http"
	"testing"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
)

const DEBUG = true

func TestConnectionQuery(t *testing.T) {
	assert := assert.New(t)
	testTracer, testTransport := getTestTracer()
	testTracer.DebugLoggingEnabled = DEBUG
	RegisterTracedDriver("MySQL", "mysql-test", &mysql.MySQLDriver{}, testTracer)

	db, err := sql.Open("MySQL", "test:test@tcp(127.0.0.1:53306)/test")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	rows, err := db.Query("select emp_no, first_name from employees limit 1")
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	testTracer.FlushTraces()
	traces := testTransport.Traces()
	assert.Len(traces, 1)
	spans := traces[0]
	assert.Len(spans, 1)
	actualSpan := spans[0]
	expectedSpan := &tracer.Span{
		Name:     "MySQL.connection.query",
		Service:  "mysql-test",
		Resource: "select emp_no, first_name from employees limit 1",
	}
	expectedSpan.SetMeta("sql.query", "select emp_no, first_name from employees limit 1")
	expectedSpan.SetMeta("args", "[]")
	expectedSpan.SetMeta("args_length", "0")
	compareSpan(t, expectedSpan, actualSpan)
}

// Test all fields of the span
func compareSpan(t *testing.T, expectedSpan, actualSpan *tracer.Span) {
	assert := assert.New(t)
	assert.Equal(expectedSpan.Name, actualSpan.Name)
	assert.Equal(expectedSpan.Service, actualSpan.Service)
	assert.Equal(expectedSpan.Resource, actualSpan.Resource)
	assert.Equal(expectedSpan.GetMeta("sql.query"), actualSpan.GetMeta("sql.query"))
	assert.Equal(expectedSpan.GetMeta("args"), actualSpan.GetMeta("args"))
	assert.Equal(expectedSpan.GetMeta("args_length"), actualSpan.GetMeta("args_length"))
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

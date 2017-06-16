package gocqltrace

import (
	"context"
	"net/http"
	"testing"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/ext"
	"github.com/gocql/gocql"
	"github.com/stretchr/testify/assert"
)

const (
	debug = false
)

// TestMain sets up the Keyspace and table if they do not exist
func TestMain(m *testing.M) {
	cluster := gocql.NewCluster("127.0.0.1")
	session, _ := cluster.CreateSession()

	// Ensures test keyspace and table person exists.
	session.Query("CREATE KEYSPACE if not exists trace WITH REPLICATION = { 'class' : 'SimpleStrategy', 'replication_factor': 1}").Exec()
	session.Query("CREATE TABLE if not exists trace.person (name text PRIMARY KEY, age int, description text)").Exec()
	session.Query("INSERT INTO trace.person (name, age, description) VALUES ('Cassandra', 100, 'A cruel mistress')").Exec()

	m.Run()
}

func TestErrorWrapper(t *testing.T) {
	assert := assert.New(t)
	testTracer, testTransport := getTestTracer()
	testTracer.DebugLoggingEnabled = debug

	cluster := gocql.NewCluster("127.0.0.1")
	session, _ := cluster.CreateSession()
	q := session.Query("CREATE KEYSPACE trace WITH REPLICATION = { 'class' : 'NetworkTopologyStrategy', 'datacenter1' : 1 };")
	err := TraceQuery("ServiceName", testTracer, q).Exec()

	testTracer.ForceFlush()
	traces := testTransport.Traces()
	assert.Len(traces, 1)
	spans := traces[0]
	assert.Len(spans, 1)
	span := spans[0]

	assert.Equal(int32(span.Error), int32(1))
	assert.Equal(span.GetMeta("error.msg"), err.Error())
	assert.Equal(span.Name, ext.CassandraQuery)
	assert.Equal(span.Resource, "CREATE KEYSPACE trace WITH REPLICATION = { 'class' : 'NetworkTopologyStrategy', 'datacenter1' : 1 };")
	assert.Equal(span.Service, "ServiceName")
	assert.Equal(span.GetMeta(ext.CassandraConsistencyLevel), "4")
	assert.Equal(span.GetMeta(ext.CassandraPaginated), "false")

	// Not added in case of an error
	assert.Equal(span.GetMeta(ext.TargetHost), "")
	assert.Equal(span.GetMeta(ext.TargetPort), "")
	assert.Equal(span.GetMeta(ext.CassandraCluster), "")
	assert.Equal(span.GetMeta(ext.CassandraKeyspace), "")
}

func TestChildWrapperSpan(t *testing.T) {
	assert := assert.New(t)
	testTracer, testTransport := getTestTracer()
	testTracer.DebugLoggingEnabled = debug

	// Parent span
	ctx := context.Background()
	parent_span := testTracer.NewChildSpanFromContext("parent_span", ctx)
	ctx = tracer.ContextWithSpan(ctx, parent_span)

	cluster := gocql.NewCluster("127.0.0.1")
	session, _ := cluster.CreateSession()
	q := session.Query("SELECT * from trace.person")
	tq := TraceQuery("Test_service_name", testTracer, q)
	tq.WithContext(ctx).Exec()
	parent_span.Finish()

	testTracer.ForceFlush()
	traces := testTransport.Traces()
	assert.Len(traces, 1)
	spans := traces[0]
	assert.Len(spans, 2)

	child_span := spans[0]
	pspan := spans[1]
	assert.Equal(pspan.Name, "parent_span")
	assert.Equal(child_span.ParentID, pspan.SpanID)
	assert.Equal(child_span.Name, ext.CassandraQuery)
	assert.Equal(child_span.Resource, "SELECT * from trace.person")
	assert.Equal(child_span.GetMeta(ext.CassandraKeyspace), "trace")

	// Will work only after gocql fix (PR #918)
	// assert.Equal(child_span.GetMeta(ext.TargetPort), "9042")
	// assert.Equal(child_span.GetMeta(ext.TargetHost), "127.0.0.1")
	// assert.Equal(child_span.GetMeta(ext.CassandraCluster), "datacenter1")
}

// getTestTracer returns a Tracer with a DummyTransport
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

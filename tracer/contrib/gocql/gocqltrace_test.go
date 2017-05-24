package gocqltrace

import (
	"context"
	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/ext"
	//"github.com/gocql/gocql"
	"github.com/gocql/gocql"
	"github.com/stretchr/testify/assert"
	"testing"
)

const (
	debug = false
)

func TestErrorWrapper(t *testing.T) {
	assert := assert.New(t)
	testTracer, testTransport := getTestTracer()
	testTracer.DebugLoggingEnabled = debug

	cluster := gocql.NewCluster("127.0.0.1")
	session, _ := cluster.CreateSession()
	q := session.Query("CREATE KEYSPACE TestKeySpace WITH REPLICATION = { 'class' : 'NetworkTopologyStrategy', 'datacenter1' : 1 };")
	TraceQuery("Test_service_name", testTracer, q).Exec()
	q = session.Query("CREATE KEYSPACE TestKeySpace WITH REPLICATION = { 'class' : 'NetworkTopologyStrategy', 'datacenter1' : 1 };")
	err := TraceQuery("Test_service_name", testTracer, q).Exec()

	testTracer.FlushTraces()
	traces := testTransport.Traces()
	assert.Len(traces, 2)
	spans := traces[1]
	assert.Len(spans, 1)
	span := spans[0]

	assert.Equal(int32(span.Error), int32(1))
	assert.Equal(span.GetMeta("error.msg"), err.Error())
	assert.Equal(span.Name, ext.CassandraQuery)
	assert.Equal(span.Resource, "CREATE KEYSPACE TestKeySpace WITH REPLICATION = { 'class' : 'NetworkTopologyStrategy', 'datacenter1' : 1 };")
	assert.Equal(span.Service, "Test_service_name")
	assert.Equal(span.GetMeta(ext.CassandraConsistencyLevel), "4")
	assert.Equal(span.GetMeta(ext.CassandraPaginated), "false")

	// Not Working
	assert.Equal(span.GetMeta(ext.TargetHost), "")
	assert.Equal(span.GetMeta(ext.TargetPort), "")
	assert.Equal(span.GetMeta(ext.CassandraCluster), "")
	assert.Equal(span.GetMeta(ext.CassandraKeyspace), "")
}

func TestError(t *testing.T) {
	assert := assert.New(t)
	testTracer, testTransport := getTestTracer()
	testTracer.DebugLoggingEnabled = debug

	cluster := NewTracedCluster("127.0.0.1")
	session, _ := cluster.CreateTracedSession("Test_service_name", testTracer)
	q := session.Query("CREATE KEYSPACE TestKeySpace WITH REPLICATION = { 'class' : 'NetworkTopologyStrategy', 'datacenter1' : 1 };")
	q.Exec()
	q = session.Query("CREATE KEYSPACE TestKeySpace WITH REPLICATION = { 'class' : 'NetworkTopologyStrategy', 'datacenter1' : 1 };")
	err := q.Exec()

	testTracer.FlushTraces()
	traces := testTransport.Traces()
	assert.Len(traces, 2)
	spans := traces[1]
	assert.Len(spans, 1)
	span := spans[0]

	assert.Equal(int32(span.Error), int32(1))
	assert.Equal(span.GetMeta("error.msg"), err.Error())
	assert.Equal(span.Name, ext.CassandraQuery)
	assert.Equal(span.Resource, "CREATE KEYSPACE TestKeySpace WITH REPLICATION = { 'class' : 'NetworkTopologyStrategy', 'datacenter1' : 1 };")
	assert.Equal(span.Service, "Test_service_name")
	assert.Equal(span.GetMeta(ext.CassandraConsistencyLevel), "4")
	assert.Equal(span.GetMeta(ext.CassandraPaginated), "false")
	assert.Equal(span.GetMeta(ext.TargetPort), "9042")

	// Not Working
	assert.Equal(span.GetMeta(ext.TargetHost), "")
	assert.Equal(span.GetMeta(ext.CassandraCluster), "")
	assert.Equal(span.GetMeta(ext.CassandraKeyspace), "")

}

func TestChildSPan(t *testing.T) {
	assert := assert.New(t)
	testTracer, testTransport := getTestTracer()
	testTracer.DebugLoggingEnabled = debug

	// Parent span
	ctx := context.Background()
	parent_span := testTracer.NewChildSpanFromContext("parent_span", ctx)
	ctx = tracer.ContextWithSpan(ctx, parent_span)

	cluster := NewTracedCluster("127.0.0.1")
	session, _ := cluster.CreateTracedSession("Test_service_name", testTracer)
	q := session.Query("CREATE KEYSPACE TestKeySpace WITH REPLICATION = { 'class' : 'NetworkTopologyStrategy', 'datacenter1' : 1 };")
	q.WithContext(ctx).Exec()
	parent_span.Finish()

	testTracer.FlushTraces()
	traces := testTransport.Traces()
	assert.Len(traces, 1)
	spans := traces[0]
	assert.Len(spans, 2)

	child_span := spans[0]
	pspan := spans[1]
	assert.Equal(pspan.Name, "parent_span")
	assert.Equal(child_span.ParentID, pspan.SpanID)
	assert.Equal(child_span.Name, ext.CassandraQuery)
	assert.Equal(child_span.GetMeta(ext.TargetPort), "9042")
	assert.Equal(child_span.Resource, "CREATE KEYSPACE TestKeySpace WITH REPLICATION = { 'class' : 'NetworkTopologyStrategy', 'datacenter1' : 1 };")
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
	q := session.Query("CREATE KEYSPACE TestKeySpace WITH REPLICATION = { 'class' : 'NetworkTopologyStrategy', 'datacenter1' : 1 };")
	tq := TraceQuery("Test_service_name", testTracer, q)
	tq.WithContext(ctx).Exec()
	parent_span.Finish()

	testTracer.FlushTraces()
	traces := testTransport.Traces()
	assert.Len(traces, 1)
	spans := traces[0]
	assert.Len(spans, 2)

	child_span := spans[0]
	pspan := spans[1]
	assert.Equal(pspan.Name, "parent_span")
	assert.Equal(child_span.ParentID, pspan.SpanID)
	assert.Equal(child_span.Name, ext.CassandraQuery)
	assert.Equal(child_span.GetMeta(ext.TargetPort), "")
	assert.Equal(child_span.Resource, "CREATE KEYSPACE TestKeySpace WITH REPLICATION = { 'class' : 'NetworkTopologyStrategy', 'datacenter1' : 1 };")
}

// getTestTracer returns a Tracer with a DummyTransport
func getTestTracer() (*tracer.Tracer, *tracer.DummyTransport) {
	transport := &tracer.DummyTransport{}
	tracer := tracer.NewTracerTransport(transport)
	return tracer, transport
}

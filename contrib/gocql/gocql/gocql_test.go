package gocql

import (
	"context"
	"log"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/gocql/gocql"
	"github.com/stretchr/testify/assert"
)

const (
	debug         = false
	cassandraHost = "127.0.0.1:9042"
)

func newCassandraCluster() *gocql.ClusterConfig {
	cluster := gocql.NewCluster(cassandraHost)
	// the InitialHostLookup must be disabled in newer versions of
	// gocql otherwise "no connections were made when creating the session"
	// error is returned for Cassandra misconfiguration (that we don't need
	// since we're testing another behavior and not the client).
	// Check: https://github.com/gocql/gocql/issues/946
	cluster.DisableInitialHostLookup = true
	return cluster
}

// TestMain sets up the Keyspace and table if they do not exist
func TestMain(m *testing.M) {
	cluster := newCassandraCluster()
	session, err := cluster.CreateSession()
	if err != nil {
		log.Fatalf("skipping test: %v\n", err)
	}
	// Ensures test keyspace and table person exists.
	session.Query("CREATE KEYSPACE if not exists trace WITH REPLICATION = { 'class' : 'SimpleStrategy', 'replication_factor': 1}").Exec()
	session.Query("CREATE TABLE if not exists trace.person (name text PRIMARY KEY, age int, description text)").Exec()
	session.Query("INSERT INTO trace.person (name, age, description) VALUES ('Cassandra', 100, 'A cruel mistress')").Exec()

	m.Run()
}

func TestErrorWrapper(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	cluster := newCassandraCluster()
	session, err := cluster.CreateSession()
	assert.Nil(err)
	q := session.Query("CREATE KEYSPACE trace WITH REPLICATION = { 'class' : 'NetworkTopologyStrategy', 'datacenter1' : 1 };")
	err = WrapQuery(q, WithServiceName("ServiceName")).Exec()

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	span := spans[0]

	assert.Equal(span.Tag(ext.Error).(error), err)
	assert.Equal(span.OperationName(), ext.CassandraQuery)
	assert.Equal(span.Tag(ext.ResourceName), "CREATE KEYSPACE trace WITH REPLICATION = { 'class' : 'NetworkTopologyStrategy', 'datacenter1' : 1 };")
	assert.Equal(span.Tag(ext.ServiceName), "ServiceName")
	assert.Equal(span.Tag(ext.CassandraConsistencyLevel), "4")
	assert.Equal(span.Tag(ext.CassandraPaginated), "false")

	// Not added in case of an error
	assert.Equal(span.Tag(ext.TargetHost), "")
	assert.Equal(span.Tag(ext.TargetPort), "")
	assert.Equal(span.Tag(ext.CassandraCluster), "")
	assert.Equal(span.Tag(ext.CassandraKeyspace), "")
}

func TestChildWrapperSpan(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	// Parent span
	parentSpan, ctx := tracer.StartSpanFromContext(context.Background(), "parentSpan")
	cluster := newCassandraCluster()
	session, err := cluster.CreateSession()
	assert.Nil(err)
	q := session.Query("SELECT * from trace.person")
	tq := WrapQuery(q, WithServiceName("TestServiceName"))
	tq.WithContext(ctx).Exec()
	parentSpan.Finish()

	spans := mt.FinishedSpans()
	assert.Len(spans, 2)

	var childSpan, pSpan mocktracer.Span
	if spans[0].ParentID() == spans[1].SpanID() {
		childSpan = spans[0]
		pSpan = spans[1]
	} else {
		childSpan = spans[1]
		pSpan = spans[0]
	}
	assert.Equal(pSpan.OperationName(), "parentSpan")
	assert.Equal(childSpan.ParentID(), pSpan.SpanID)
	assert.Equal(childSpan.OperationName(), ext.CassandraQuery)
	assert.Equal(childSpan.Tag(ext.ResourceName), "SELECT * from trace.person")
	assert.Equal(childSpan.Tag(ext.CassandraKeyspace), "trace")
	assert.Equal(childSpan.Tag(ext.TargetPort), "9042")
	assert.Equal(childSpan.Tag(ext.TargetHost), "127.0.0.1")
	assert.Equal(childSpan.Tag(ext.CassandraCluster), "datacenter1")
}

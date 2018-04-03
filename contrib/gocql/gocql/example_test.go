package gocql_test

import (
	"context"

	"github.com/gocql/gocql"
	gocqltrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/gocql/gocql"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// To trace Cassandra commands, use our query wrapper WrapQuery.
func Example() {
	// Initialise a Cassandra session as usual, create a query.
	cluster := gocql.NewCluster("127.0.0.1")
	session, _ := cluster.CreateSession()
	query := session.Query("CREATE KEYSPACE if not exists trace WITH REPLICATION = { 'class' : 'SimpleStrategy', 'replication_factor': 1}")

	// Use context to pass information down the call chain
	_, ctx := tracer.StartSpanFromContext(context.Background(), "parent.request",
		tracer.SpanType(ext.AppTypeDB),
		tracer.ServiceName("web"),
		tracer.ResourceName("/home"),
	)

	// Wrap the query to trace it and pass the context for inheritance
	tracedQuery := gocqltrace.WrapQuery(query, gocqltrace.WithServiceName("ServiceName"))
	tracedQuery.WithContext(ctx)

	// Execute your query as usual
	tracedQuery.Exec()
}

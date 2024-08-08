// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package gocql_test

import (
	"context"
	"log"

	"github.com/gocql/gocql"

	gocqltrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/gocql/gocql"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func ExampleNewCluster() {
	// Initialise a wrapped Cassandra session and create a query.
	cluster := gocqltrace.NewCluster([]string{"127.0.0.1:9043"}, gocqltrace.WithServiceName("ServiceName"))
	session, _ := cluster.CreateSession()
	query := session.Query("CREATE KEYSPACE if not exists trace WITH REPLICATION = { 'class' : 'SimpleStrategy', 'replication_factor': 1}")

	// Use context to pass information down the call chain
	_, ctx := tracer.StartSpanFromContext(context.Background(), "parent.request",
		tracer.SpanType(ext.SpanTypeCassandra),
		tracer.ServiceName("web"),
		tracer.ResourceName("/home"),
	)

	// Wrap the query to trace it and pass the context for inheritance
	query.WithContext(ctx)
	// Provide any options for the specific query.
	query.WithWrapOptions(gocqltrace.WithResourceName("CREATE KEYSPACE"))

	// Execute your query as usual
	query.Exec()
}

func ExampleNewTracedSession() {
	cluster := gocql.NewCluster("127.0.0.1:9042")
	cluster.Keyspace = "my-keyspace"

	// Create a new traced session using any number of options
	session, err := gocqltrace.NewTracedSession(cluster, gocqltrace.WithServiceName("ServiceName"))
	if err != nil {
		log.Fatal(err)
	}
	query := session.Query("CREATE KEYSPACE if not exists trace WITH REPLICATION = { 'class' : 'SimpleStrategy', 'replication_factor': 1}")

	// Use context to pass information down the call chain
	_, ctx := tracer.StartSpanFromContext(context.Background(), "parent.request",
		tracer.SpanType(ext.SpanTypeCassandra),
		tracer.ServiceName("web"),
		tracer.ResourceName("/home"),
	)
	query.WithContext(ctx)

	// Finally, execute the query
	if err := query.Exec(); err != nil {
		log.Fatal(err)
	}
}

func ExampleTraceQuery() {
	cluster := gocql.NewCluster("127.0.0.1:9042")
	cluster.Keyspace = "my-keyspace"

	// Create a new regular gocql session
	session, err := cluster.CreateSession()
	if err != nil {
		log.Fatal(err)
	}
	query := session.Query("CREATE KEYSPACE if not exists trace WITH REPLICATION = { 'class' : 'SimpleStrategy', 'replication_factor': 1}")

	// Use context to pass information down the call chain
	_, ctx := tracer.StartSpanFromContext(context.Background(), "parent.request",
		tracer.SpanType(ext.SpanTypeCassandra),
		tracer.ServiceName("web"),
		tracer.ResourceName("/home"),
	)
	query.WithContext(ctx)

	// Enable tracing this query only.
	query = gocqltrace.TraceQuery(query, cluster, gocqltrace.WithServiceName("ServiceName"))

	// Finally, execute the query
	if err := query.Exec(); err != nil {
		log.Fatal(err)
	}
}

func ExampleTraceBatch() {
	cluster := gocql.NewCluster("127.0.0.1:9042")
	cluster.Keyspace = "my-keyspace"

	// Create a new regular gocql session
	session, err := cluster.CreateSession()
	if err != nil {
		log.Fatal(err)
	}

	// Create a new regular gocql batch and add some queries to it
	stmt := "INSERT INTO trace.person (name, age, description) VALUES (?, ?, ?)"
	batch := session.NewBatch(gocql.UnloggedBatch)
	batch.Query(stmt, "Kate", 80, "Cassandra's sister running in kubernetes")
	batch.Query(stmt, "Lucas", 60, "Another person")

	// Use context to pass information down the call chain
	_, ctx := tracer.StartSpanFromContext(context.Background(), "parent.request",
		tracer.SpanType(ext.SpanTypeCassandra),
		tracer.ServiceName("web"),
		tracer.ResourceName("/home"),
	)
	batch.WithContext(ctx)

	// Enable tracing this batch only
	batch = gocqltrace.TraceBatch(batch, cluster, gocqltrace.WithServiceName("ServiceName"))

	// Finally, execute the batch
	if err := session.ExecuteBatch(batch); err != nil {
		log.Fatal(err)
	}
}

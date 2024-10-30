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

func ExampleCreateTracedSession() {
	cluster := gocql.NewCluster("127.0.0.1:9042")
	cluster.Keyspace = "my-keyspace"

	// Create a new traced session using any number of options
	session, err := gocqltrace.CreateTracedSession(cluster, gocqltrace.WithServiceName("ServiceName"))
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

	// If you don't want a concrete query to be traced, you can do query.Observer(nil)

	// Finally, execute the query
	if err := query.Exec(); err != nil {
		log.Fatal(err)
	}
}

func ExampleNewObserver() {
	cluster := gocql.NewCluster("127.0.0.1:9042")
	cluster.Keyspace = "my-keyspace"

	// Create a new regular gocql session
	session, err := cluster.CreateSession()
	if err != nil {
		log.Fatal(err)
	}
	// Create a new observer using same set of options as gocqltrace.CreateTracedSession.
	obs := gocqltrace.NewObserver(cluster, gocqltrace.WithServiceName("ServiceName"))

	// Attach the observer to queries / batches individually.
	tracedQuery := session.Query("SELECT something FROM somewhere").Observer(obs)
	untracedQuery := session.Query("SELECT something FROM somewhere")

	// Use context to pass information down the call chain
	_, ctx := tracer.StartSpanFromContext(context.Background(), "parent.request",
		tracer.SpanType(ext.SpanTypeCassandra),
		tracer.ServiceName("web"),
		tracer.ResourceName("/home"),
	)
	tracedQuery.WithContext(ctx)

	// Finally, execute the query
	if err := tracedQuery.Exec(); err != nil {
		log.Fatal(err)
	}
	if err := untracedQuery.Exec(); err != nil {
		log.Fatal(err)
	}
}

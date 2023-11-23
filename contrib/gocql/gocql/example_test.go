// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package gocql_test

import (
	"context"

	gocqltrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/gocql/gocql"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func Example() {
	// Initialise a wrapped Cassandra session and create a query.
	cluster := gocqltrace.NewCluster([]string{"127.0.0.1"}, gocqltrace.WithServiceName("ServiceName"))
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

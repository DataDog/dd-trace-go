// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"io/ioutil"
	"log"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
)

// A basic example demonstrating how to start the tracer, as well as how
// to create a root span and a child span that is a descendant of it.
func Example() {
	// Start the tracer and defer the Stop method.
	Start(WithAgentAddr("host:port"))
	defer Stop()

	// Start a root span.
	span := StartSpan("get.data")
	defer span.Finish()

	// Create a child of it, computing the time needed to read a file.
	child := StartSpan("read.file", ChildOf(span.Context()))
	child.SetTag(ext.ResourceName, "test.json")

	// Perform an operation.
	_, err := ioutil.ReadFile("~/test.json")

	// We may finish the child span using the returned error. If it's
	// nil, it will be disregarded.
	child.Finish(WithError(err))
	if err != nil {
		log.Fatal(err)
	}
}

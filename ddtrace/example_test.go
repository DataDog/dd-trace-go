// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package ddtrace_test

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// The below example illustrates a simple use case using the "tracer" package,
// our native Datadog APM tracing client integration. For thorough documentation
// and further examples, visit its own godoc page.
func Example_datadog() {
	// Start the tracer and defer the Stop method.
	tracer.Start(tracer.WithAgentAddr("host:port"))
	defer tracer.Stop()

	// If you expect your application to be shutdown via SIGTERM (e.g. a container in k8s)
	// You likely want to listen for that signal and stop the tracer to ensure no data is lost
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM)
	go func() {
		<-sigChan
		tracer.Stop()
	}()

	// Start a root span.
	span := tracer.StartSpan("get.data")
	defer span.Finish()

	// Create a child of it, computing the time needed to read a file.
	child := span.StartChild("read.file")
	child.SetTag(ext.ResourceName, "test.json")

	fmt.Printf("128 bit trace id = %s\n", child.Context().TraceID())

	// Perform an operation.
	_, err := os.ReadFile("~/test.json")

	// We may finish the child span using the returned error. If it's
	// nil, it will be disregarded.
	child.Finish(tracer.WithError(err))
	if err != nil {
		log.Fatal(err)
	}
}

// The code below illustrates a scenario of how one could use a mock tracer in tests
// to assert that spans are created correctly.
func Example_mocking() {
	// Setup the test environment: start the mock tracer.
	mt := mocktracer.Start()
	defer mt.Stop()

	// Run test code: in this example we will simply create a span to illustrate.
	tracer.StartSpan("test.span").Finish()

	// Assert the results: query the mock tracer for finished spans.
	spans := mt.FinishedSpans()
	if len(spans) != 1 {
		// fail
		panic("expected 1 span")
	}
	if spans[0].OperationName() != "test.span" {
		// fail
		panic("unexpected operation name")
	}
}

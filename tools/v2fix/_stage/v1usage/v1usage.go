// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package main

import (
	"fmt"
	"log"
	"os"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func main() {
	// Start the tracer and defer the Stop method.
	tracer.Start( // want `gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer.Start`
		tracer.WithAgentAddr("host:port"), // want `gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer.WithAgentAddr`
	)
	defer tracer.Stop() // want `gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer.Stop`

	// Start a root span.
	span := tracer.StartSpan("get.data") // want `gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer.StartSpan`
	defer span.Finish()                  // want `\(gopkg.in/DataDog/dd-trace-go.v1/ddtrace.Span\).Finish`

	// Create a child of it, computing the time needed to read a file.
	child := tracer.StartSpan( // want `gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer.StartSpan`
		"read.file",
		tracer.ChildOf( // want `gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer.ChildOf`
			span.Context(), // want `\(gopkg.in/DataDog/dd-trace-go.v1/ddtrace.Span\).Context`
		),
	)
	child.SetTag(ext.ResourceName, "test.json") // want `\(gopkg.in/DataDog/dd-trace-go.v1/ddtrace.Span\).SetTag`

	// If you are using 128 bit trace ids and want to generate the high
	// order bits, cast the span's context to ddtrace.SpanContextW3C.
	// See Issue #1677
	if w3Cctx, ok := child.Context().(ddtrace.SpanContextW3C); ok { // want `\(gopkg.in/DataDog/dd-trace-go.v1/ddtrace.Span\).Context`
		fmt.Printf("128 bit trace id = %s\n", w3Cctx.TraceID128()) // want `\(gopkg.in/DataDog/dd-trace-go.v1/ddtrace.SpanContextW3C\).TraceID128`
	}

	// Perform an operation.
	_, err := os.ReadFile("~/test.json")

	// We may finish the child span using the returned error. If it's
	// nil, it will be disregarded.
	child.Finish( // want `\(gopkg.in/DataDog/dd-trace-go.v1/ddtrace.Span\).Finish`
		tracer.WithError(err), // want `gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer.WithError`
	)
	if err != nil {
		log.Fatal(err)
	}
}

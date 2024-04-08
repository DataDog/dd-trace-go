// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package main

import (
	"fmt"
	"log"
	"os"

	"github.com/DataDog/dd-trace-go/v2/ddtrace"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func main() {
	// Start the tracer and defer the Stop method.
	tracer.Start( // want `github.com/DataDog/dd-trace-go/v2/ddtrace/tracer.Start`
		tracer.WithAgentAddr("host:port"), // want `github.com/DataDog/dd-trace-go/v2/ddtrace/tracer.WithAgentAddr`
	)
	defer tracer.Stop() // want `github.com/DataDog/dd-trace-go/v2/ddtrace/tracer.Stop`

	// Start a root span.
	span := tracer.StartSpan("get.data") // want `github.com/DataDog/dd-trace-go/v2/ddtrace/tracer.StartSpan`
	defer span.Finish()                  // want `\(github.com/DataDog/dd-trace-go/v2/ddtrace.Span\).Finish`

	// Create a child of it, computing the time needed to read a file.
	child := tracer.StartSpan( // want `github.com/DataDog/dd-trace-go/v2/ddtrace/tracer.StartSpan`
		"read.file",
		tracer.ChildOf( // want `github.com/DataDog/dd-trace-go/v2/ddtrace/tracer.ChildOf`
			span.Context(), // want `\(github.com/DataDog/dd-trace-go/v2/ddtrace.Span\).Context`
		),
	)
	child.SetTag(ext.ResourceName, "test.json") // want `\(github.com/DataDog/dd-trace-go/v2/ddtrace.Span\).SetTag`

	// If you are using 128 bit trace ids and want to generate the high
	// order bits, cast the span's context to ddtrace.SpanContextW3C.
	// See Issue #1677
	if w3Cctx, ok := child.Context().(ddtrace.SpanContextW3C); ok { // want `\(github.com/DataDog/dd-trace-go/v2/ddtrace.Span\).Context`
		fmt.Printf("128 bit trace id = %s\n", w3Cctx.TraceID128()) // want `\(github.com/DataDog/dd-trace-go/v2/ddtrace.SpanContextW3C\).TraceID128`
	}

	// Perform an operation.
	_, err := os.ReadFile("~/test.json")

	// We may finish the child span using the returned error. If it's
	// nil, it will be disregarded.
	child.Finish( // want `\(github.com/DataDog/dd-trace-go/v2/ddtrace.Span\).Finish`
		tracer.WithError(err), // want `github.com/DataDog/dd-trace-go/v2/ddtrace/tracer.WithError`
	)
	if err != nil {
		log.Fatal(err)
	}
}

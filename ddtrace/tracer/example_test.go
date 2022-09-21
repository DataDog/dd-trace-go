// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer_test

import (
	"context"
	"fmt"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// A basic example demonstrating how to start the tracer, as well as how
// to create a root span and a child span that is a descendant of it.
func Example() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// Start the tracer and defer the Stop method.
	tracer.Start()
	defer tracer.Stop()

	// Start a root span.
	span, ctx := tracer.StartSpanFromContext(ctx, "parent")
	defer span.Finish()

	// Run some code.
	err := doSomething(ctx)
	if err != nil {
		panic(err)
	}
}

func doSomething(ctx context.Context) (err error) {
	// Create a child, using the context of the parent span.
	span, ctx := tracer.StartSpanFromContext(ctx, "do.something", tracer.Tag(ext.ResourceName, "alarm"))
	defer func() {
		span.Finish(tracer.WithError(err))
	}()

	// Perform an operation.
	select {
	case <-time.After(5 * time.Millisecond):
		fmt.Println("ding!")
	case <-ctx.Done():
		fmt.Println("timed out :(")
	}
	return ctx.Err()
}

// The code below illustrates how to set up a Post Processor in order to drop and/or modify traces.
func Example_processor() {
	// This processor will drop traces that do not contain an error, db span or client http request
	// to endpoint GET /api/v1. In the case there is a http request to endpoint GET /api/v1, it will add
	// a span tag to the local root span.
	tracer.Start(tracer.WithPostProcessor(func(spans []tracer.ReadWriteSpan) []tracer.ReadWriteSpan {
		for _, s := range spans {
			// trace contains an error which isn't "specific error".
			if s.IsError() && s.Tag("error.message") != "specific error" {
				return spans
			}
			// trace contains a db request.
			if s.Tag("span.type") == "db" {
				return spans
			}
			// trace contains a http request to endpoint GET /api/v1.
			if s.Tag("service.name") == "service-a-http-client" && s.Tag("resource.name") == "GET /api/v1" {
				// set tag on local root span.
				spans[0].SetTag("calls.external.service", "service-b-api")
				return spans
			}
		}
		return nil
	}))
	defer tracer.Stop()
}

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
	doSomething(ctx)
}

func doSomething(ctx context.Context) {
	// Create a child, using the context of the parent span.
	opts := []tracer.StartSpanOption{
		tracer.Tag(ext.ResourceName, "alarm"),
	}
	span, ctx := tracer.StartSpanFromContext(ctx, "do.something", opts...)
	defer func() {
		span.Finish(tracer.WithError(ctx.Err()))
	}()

	// Perform an operation.
	select {
	case <-time.After(5 * time.Millisecond):
		fmt.Println("ding!")
	case <-ctx.Done():
		fmt.Println("overslept :(")
	}
}

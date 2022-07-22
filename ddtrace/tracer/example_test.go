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
	span, ctx := tracer.StartSpanFromContext(ctx, "your.work")
	defer span.Finish()

	// Create a child, using the context of the parent span.
	child, ctx := tracer.StartSpanFromContext(ctx, "this.service")
	child.SetTag(ext.ResourceName, "alarm")

	// Run some code.
	err := yourCode(ctx)

	// Finish the span.
	child.Finish(tracer.WithError(err))
}

func yourCode(ctx context.Context) error {
	// Perform an operation.
	select {
	case <-time.After(5 * time.Millisecond):
		fmt.Println("ding!")
	case <-ctx.Done():
		fmt.Println("overslept :(")
	}
	return ctx.Err()
}

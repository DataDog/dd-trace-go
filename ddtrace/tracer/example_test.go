// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer_test

import (
	"context"
	"log"
	"math/rand"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// A basic example demonstrating how to start the tracer, as well as how
// to create a root span and a child span that is a descendant of it.
func Example() {
	ctx := context.Background()

	// Start the tracer and defer the Stop method.
	tracer.Start()
	defer tracer.Stop()

	// Start a root span.
	span, ctx := tracer.StartSpanFromContext(ctx, "your.work")
	defer span.Finish()

	yourCode := func(ctx context.Context) error {
		// Perform an operation.
		b := make([]byte, 100000)
		_, err := rand.Read(b)
		if err != nil {
			log.Fatal(err)
		}
		return err
	}

	// Create a child, using the context of the parent span.
	child, ctx := tracer.StartSpanFromContext(ctx, "this.service")
	child.SetTag(ext.ResourceName, "randomization")

	// Run some code.
	err := yourCode(ctx)

	// Finish the span.
	child.Finish(tracer.WithError(err))
}

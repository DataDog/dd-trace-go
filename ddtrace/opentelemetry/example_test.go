// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package opentelemetry_test

import (
	"context"
	"log"
	"os"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	ddotel "github.com/DataDog/dd-trace-go/v2/ddtrace/opentelemetry"
	ddtracer "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

func Example() {
	// Create a TracerProvider, optionally providing a set of options,
	// that are specific to Datadog's APM product, and defer the Shutdown method, which stops the tracer.
	provider := ddotel.NewTracerProvider(ddtracer.WithProfilerCodeHotspots(true))
	defer provider.Shutdown()

	// Use it with the OpenTelemetry API to set the global TracerProvider.
	otel.SetTracerProvider(provider)

	// Start the Tracer with the OpenTelemetry API.
	t := otel.Tracer("")

	// Start the OpenTelemetry Span, optionally providing a set of options,
	// that are specific to Datadog's APM product.
	ctx, parent := t.Start(ddotel.ContextWithStartOptions(context.Background(), ddtracer.Measured()), "span_name")
	defer parent.End()

	// Create a child of the parent span, computing the time needed to read a file.
	ctx, child := t.Start(ctx, "read.file")
	child.SetAttributes(attribute.String(ext.ResourceName, "test.json"))

	// Perform an operation.
	_, err := os.ReadFile("~/test.json")

	// We may finish the child span using the returned error. If it's
	// nil, it will be disregarded.
	ddotel.EndOptions(child, ddtracer.WithError(err))
	child.End()
	if err != nil {
		log.Fatal(err)
	}
}

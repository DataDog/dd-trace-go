// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package opentelemetry_test

import (
	"context"
	"go.opentelemetry.io/otel"
	ddotel "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/opentelemetry"
	ddtracer "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func Example() {
	// Create a TracerProvider, optionally providing a set of options,
	// that are specific to Datadog's APM product.
	provider := ddotel.NewTracerProvider(ddtracer.WithService("opentelemetry_service"))

	// Use it with the OpenTelemetry API to set the global TracerProvider.
	otel.SetTracerProvider(provider)

	// Start the Tracer with the OpenTelemetry API.
	t := otel.Tracer("")

	// Start the OpenTelemetry Span, optionally providing a set of options,
	// that are specific to Datadog's APM product.
	_, sp := t.Start(ddotel.ContextWithStartOptions(context.Background(),
		ddtracer.ResourceName("resource_name")), "span_name")
	sp.End()
}

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package opentelemetry

import (
	"context"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func BenchmarkOTelHTTPClientSpan(b *testing.B) {
	tp := NewTracerProvider(tracer.WithEnv("test_env"), tracer.WithService("test_srv"))
	defer tp.Shutdown()
	otel.SetTracerProvider(tp)
	tr := otel.Tracer("")

	ctx := context.Background()
	startOpts := []oteltrace.SpanStartOption{
		oteltrace.WithSpanKind(oteltrace.SpanKindClient),
		oteltrace.WithAttributes(
			attribute.String("http.request.method", "GET"),
			attribute.String("url.full", "https://api.example.com/v1/items"),
			attribute.String("server.address", "api.example.com"),
		),
	}

	b.ResetTimer()
	for b.Loop() {
		_, sp := tr.Start(ctx, "GET /v1/items", startOpts...)
		sp.SetAttributes(attribute.Int("http.response.status_code", 200))
		sp.End()
	}
}

func BenchmarkOTelChildSpan(b *testing.B) {
	tp := NewTracerProvider(tracer.WithEnv("test_env"), tracer.WithService("test_srv"))
	defer tp.Shutdown()
	otel.SetTracerProvider(tp)
	tr := otel.Tracer("")

	parentCtx, parent := tr.Start(context.Background(), "parent_op")
	defer parent.End()

	b.ResetTimer()
	for b.Loop() {
		_, sp := tr.Start(parentCtx, "child_op")
		sp.End()
	}
}

func BenchmarkOTelSpanContext(b *testing.B) {
	tp := NewTracerProvider(tracer.WithEnv("test_env"), tracer.WithService("test_srv"))
	defer tp.Shutdown()
	otel.SetTracerProvider(tp)
	tr := otel.Tracer("")

	_, sp := tr.Start(context.Background(), "op_name")
	defer sp.End()

	b.ResetTimer()
	for b.Loop() {
		_ = sp.SpanContext()
	}
}

func BenchmarkOTelSemanticsSpan(b *testing.B) {
	b.Setenv("DD_TRACE_OTEL_SEMANTICS_ENABLED", "true")
	tp := NewTracerProvider(tracer.WithEnv("test_env"), tracer.WithService("test_srv"))
	defer tp.Shutdown()
	otel.SetTracerProvider(tp)
	tr := otel.Tracer("")

	ctx := context.Background()
	b.ResetTimer()
	for b.Loop() {
		_, sp := tr.Start(ctx, "GET /v1/items", oteltrace.WithSpanKind(oteltrace.SpanKindServer))
		sp.SetAttributes(
			attribute.String("http.request.method", "GET"),
			attribute.Int("http.response.status_code", 200),
		)
		sp.SetName("GET /v1/items/{id}")
		sp.End()
	}
}

func BenchmarkOTelSpanWithEvents(b *testing.B) {
	tp := NewTracerProvider(tracer.WithEnv("test_env"), tracer.WithService("test_srv"))
	defer tp.Shutdown()
	otel.SetTracerProvider(tp)
	tr := otel.Tracer("")

	ctx := context.Background()
	b.ResetTimer()
	for b.Loop() {
		_, sp := tr.Start(ctx, "op_name")
		sp.AddEvent("cache.miss", oteltrace.WithAttributes(attribute.String("key", "item:42")))
		sp.End()
	}
}

func BenchmarkBridgeSpanFromContext(b *testing.B) {
	loadBridge()
	prev := bridgeEnabled
	bridgeEnabled = true
	defer func() { bridgeEnabled = prev }()

	tracer.Start(tracer.WithEnv("test_env"), tracer.WithService("test_srv"))
	defer tracer.Stop()

	ddSpan, ctx := tracer.StartSpanFromContext(context.Background(), "http.request")
	bindOwner(ddSpan)
	defer ddSpan.Finish()

	b.ResetTimer()
	for b.Loop() {
		sp, ok := bridgeSpanFromContext(ctx, nil)
		if !ok {
			b.Fatal("expected bridged span")
		}
		sp.SetAttributes(attribute.String("custom.key", "val"))
		_ = sp.SpanContext()
	}
}

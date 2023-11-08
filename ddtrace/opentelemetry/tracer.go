// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package opentelemetry

import (
	"context"
	"encoding/binary"
	"encoding/hex"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"

	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
)

var _ oteltrace.Tracer = (*oteltracer)(nil)

var telemetryTags = []string{`"integration_name":"otel"`}

type oteltracer struct {
	provider *TracerProvider
	ddtrace.Tracer
}

func (t *oteltracer) Start(ctx context.Context, spanName string, opts ...oteltrace.SpanStartOption) (context.Context, oteltrace.Span) {
	var ssConfig = oteltrace.NewSpanStartConfig(opts...)
	// OTel name is akin to resource name in Datadog
	ddopts := []ddtrace.StartSpanOption{tracer.ResourceName(spanName)}
	if !ssConfig.NewRoot() {
		if s, ok := tracer.SpanFromContext(ctx); ok {
			// if the span originates from the Datadog tracer,
			// inherit given span context as a parent
			ddopts = append(ddopts, tracer.ChildOf(s.Context()))
		} else if sctx := oteltrace.SpanFromContext(ctx).SpanContext(); sctx.IsValid() {
			// if the span doesn't originate from the Datadog tracer,
			// use SpanContextW3C implementation struct to pass span context information
			ddopts = append(ddopts, tracer.ChildOf(&otelCtxToDDCtx{sctx}))
		}
	}
	if t := ssConfig.Timestamp(); !t.IsZero() {
		ddopts = append(ddopts, tracer.StartTime(ssConfig.Timestamp()))
	}
	for _, attr := range ssConfig.Attributes() {
		if k, v := toSpecialAttributes(string(attr.Key), attr.Value); k != "" {
			ddopts = append(ddopts, tracer.Tag(k, v))
		}
	}
	if k := ssConfig.SpanKind(); k != 0 {
		ddopts = append(ddopts, tracer.Tag(ext.SpanKind, k.String()))
	}
	telemetry.GlobalClient.Count(telemetry.NamespaceTracers, "spans_created", 1.0, telemetryTags, true)
	if opts, ok := spanOptionsFromContext(ctx); ok {
		ddopts = append(ddopts, opts...)
	}
	// since there is no way to see if and how the span operation name was set,
	// we have to record it locally with span.nameSet field if it was changed
	// with the explicit value of 'operation.name' tag
	attributeSet := attribute.NewSet(ssConfig.Attributes()...)
	opName, opNamePresent := "", false
	if name := valueFromAttributes(attributeSet, operationNameKey); name != "" {
		opName = name
		opNamePresent = true
	} else {
		opName = remapOperationName(ssConfig.SpanKind(), attributeSet, false)
	}
	s := tracer.StartSpan(opName, ddopts...)
	os := oteltrace.Span(&span{
		Span:       s,
		nameSet:    opNamePresent,
		oteltracer: t,
		spanKind:   ssConfig.SpanKind(),
	})
	// Erase the start span options from the context to prevent them from being propagated to children
	ctx = context.WithValue(ctx, startOptsKey, nil)
	// Wrap the span in OpenTelemetry and Datadog contexts to propagate span context values
	ctx = oteltrace.ContextWithSpan(tracer.ContextWithSpan(ctx, s), os)
	return ctx, os
}

type otelCtxToDDCtx struct {
	oc oteltrace.SpanContext
}

func (c *otelCtxToDDCtx) TraceID() uint64 {
	id := c.oc.TraceID()
	return binary.BigEndian.Uint64(id[8:])
}

func (c *otelCtxToDDCtx) SpanID() uint64 {
	id := c.oc.SpanID()
	return binary.BigEndian.Uint64(id[:])
}

func (c *otelCtxToDDCtx) ForeachBaggageItem(_ func(k, v string) bool) {}

func (c *otelCtxToDDCtx) TraceID128() string {
	id := c.oc.TraceID()
	return hex.EncodeToString(id[:])
}

func (c *otelCtxToDDCtx) TraceID128Bytes() [16]byte {
	return c.oc.TraceID()
}

var _ oteltrace.Tracer = (*noopOteltracer)(nil)

type noopOteltracer struct{}

func (n *noopOteltracer) Start(_ context.Context, _ string, _ ...oteltrace.SpanStartOption) (context.Context, oteltrace.Span) {
	return nil, nil
}

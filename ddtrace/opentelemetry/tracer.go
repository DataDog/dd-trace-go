// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package opentelemetry

import (
	"context"
	"encoding/binary"
	"encoding/hex"

	oteltrace "go.opentelemetry.io/otel/trace"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

var _ oteltrace.Tracer = (*oteltracer)(nil)

type oteltracer struct {
	provider *TracerProvider
	ddtrace.Tracer
}
type otelCtxToDDCtx struct {
	oc oteltrace.SpanContext
}

func (c *otelCtxToDDCtx) TraceID() uint64 {
	allTraceID := [16]byte(c.oc.TraceID())

	return binary.BigEndian.Uint64(allTraceID[8:])
}

func (c *otelCtxToDDCtx) SpanID() uint64 {
	sid := [8]byte(c.oc.SpanID())
	return binary.BigEndian.Uint64(sid[:])
}

func (c *otelCtxToDDCtx) ForeachBaggageItem(handler func(k, v string) bool) {
	return
}

func (c *otelCtxToDDCtx) TraceID128() string {
	allTraceID := [16]byte(c.oc.TraceID())
	return hex.EncodeToString(allTraceID[:])
}

func (c *otelCtxToDDCtx) TraceID128Bytes() [16]byte {
	return c.oc.TraceID()
}

func (t *oteltracer) Start(ctx context.Context, spanName string, opts ...oteltrace.SpanStartOption) (context.Context, oteltrace.Span) {
	var ssConfig = oteltrace.NewSpanStartConfig(opts...)
	var ddopts []ddtrace.StartSpanOption
	if !ssConfig.NewRoot() {
		if sctx := oteltrace.SpanFromContext(ctx).SpanContext(); sctx.IsValid() {
			ddopts = append(ddopts, tracer.ChildOf(&otelCtxToDDCtx{sctx}))
		}
		if s, ok := tracer.SpanFromContext(ctx); ok {
			ddopts = append(ddopts, tracer.ChildOf(s.Context()))
		}
	}
	if t := ssConfig.Timestamp(); !t.IsZero() {
		ddopts = append(ddopts, tracer.StartTime(ssConfig.Timestamp()))
	}
	for _, attr := range ssConfig.Attributes() {
		ddopts = append(ddopts, tracer.Tag(string(attr.Key), attr.Value.AsInterface()))
	}
	if k := ssConfig.SpanKind(); k != 0 {
		ddopts = append(ddopts, tracer.SpanType(k.String()))
	}
	if opts, ok := spanOptionsFromContext(ctx); ok {
		ddopts = append(ddopts, opts...)
	}
	s := tracer.StartSpan(spanName, ddopts...)
	os := oteltrace.Span(&span{
		Span:       s,
		oteltracer: t,
	})
	return oteltrace.ContextWithSpan(tracer.ContextWithSpan(ctx, s), os), os
}

var _ oteltrace.Tracer = (*noopOteltracer)(nil)

type noopOteltracer struct{}

func (n *noopOteltracer) Start(ctx context.Context, spanName string, opts ...oteltrace.SpanStartOption) (context.Context, oteltrace.Span) {
	return nil, nil
}

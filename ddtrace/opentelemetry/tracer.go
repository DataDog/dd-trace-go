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

	oteltrace "go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

var _ oteltrace.Tracer = (*oteltracer)(nil)

var telemetryTags = []string{"integration_name:otel"}

type oteltracer struct {
	noop.Tracer // https://pkg.go.dev/go.opentelemetry.io/otel/trace#hdr-API_Implementations
	provider    *TracerProvider
	DD          ddtrace.Tracer
}

func (t *oteltracer) Start(ctx context.Context, spanName string, opts ...oteltrace.SpanStartOption) (context.Context, oteltrace.Span) {
	var ssConfig = oteltrace.NewSpanStartConfig(opts...)
	// OTel name is akin to resource name in Datadog
	var ddopts = []ddtrace.StartSpanOption{tracer.ResourceName(spanName)}
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
	if k := ssConfig.SpanKind(); k != 0 {
		ddopts = append(ddopts, tracer.Tag(ext.SpanKind, k.String()))
	}
	telemetry.GlobalClient.Count(telemetry.NamespaceTracers, "spans_created", 1.0, telemetryTags, true)
	var cfg ddtrace.StartSpanConfig
	cfg.Tags = make(map[string]interface{})
	for _, attr := range ssConfig.Attributes() {
		cfg.Tags[string(attr.Key)] = attr.Value.AsInterface()
	}
	if opts, ok := spanOptionsFromContext(ctx); ok {
		ddopts = append(ddopts, opts...)
		for _, o := range opts {
			o(&cfg)
		}
	}
	// Add provide OTel Span Links to the underlying Datadog span.
	if len(ssConfig.Links()) > 0 {
		links := make([]ddtrace.SpanLink, 0, len(ssConfig.Links()))
		for _, link := range ssConfig.Links() {
			ctx := otelCtxToDDCtx{link.SpanContext}
			attrs := make(map[string]string, len(link.Attributes))
			for _, attr := range link.Attributes {
				attrs[string(attr.Key)] = attr.Value.Emit()
			}
			links = append(links, ddtrace.SpanLink{
				TraceID:     ctx.TraceID(),
				TraceIDHigh: ctx.TraceIDUpper(),
				SpanID:      ctx.SpanID(),
				Tracestate:  link.SpanContext.TraceState().String(),
				Attributes:  attrs,
				// To distinguish between "not sampled" and "not set", Datadog
				// will rely on the highest bit being set. The OTel API doesn't
				// differentiate this, so we will just always mark it as set.
				Flags: uint32(link.SpanContext.TraceFlags()) | (1 << 31),
			})
		}
		ddopts = append(ddopts, tracer.WithSpanLinks(links))
	}
	// Since there is no way to see if and how the span operation name was set,
	// we have to record the attributes  locally.
	// The span operation name will be calculated when it's ended.
	s := tracer.StartSpan(spanName, ddopts...)
	os := oteltrace.Span(&span{
		DD:         s,
		oteltracer: t,
		spanKind:   ssConfig.SpanKind(),
		attributes: cfg.Tags,
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

func (c *otelCtxToDDCtx) TraceIDUpper() uint64 {
	id := c.oc.TraceID()
	return binary.BigEndian.Uint64(id[:8])
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

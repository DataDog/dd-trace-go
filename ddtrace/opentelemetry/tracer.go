// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package opentelemetry

import (
	"context"
	"encoding/binary"
	"encoding/hex"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/baggage"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"

	otelbaggage "go.opentelemetry.io/otel/baggage"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

var _ oteltrace.Tracer = (*oteltracer)(nil)

var telemetryTags = []string{"integration_name:otel"}

type oteltracer struct {
	noop.Tracer // https://pkg.go.dev/go.opentelemetry.io/otel/trace#hdr-API_Implementations
	provider    *TracerProvider
	DD          tracer.Tracer
}

func (t *oteltracer) Start(ctx context.Context, spanName string, opts ...oteltrace.SpanStartOption) (context.Context, oteltrace.Span) {
	var ssConfig = oteltrace.NewSpanStartConfig(opts...)
	// OTel name is akin to resource name in Datadog
	var ddopts = []tracer.StartSpanOption{tracer.ResourceName(spanName)}
	if !ssConfig.NewRoot() {
		if s, ok := tracer.SpanFromContext(ctx); ok {
			// if the span originates from the Datadog tracer,
			// inherit given span context as a parent
			ddopts = append(ddopts, tracer.ChildOf(s.Context()))
		} else if sctx := oteltrace.SpanFromContext(ctx).SpanContext(); sctx.IsValid() {
			// if the span doesn't originate from the Datadog tracer,
			// use SpanContextW3C implementation struct to pass span context information
			ddopts = append(ddopts, tracer.ChildOf(tracer.FromGenericCtx(&otelCtxToDDCtx{sctx})))
		}
	}
	if t := ssConfig.Timestamp(); !t.IsZero() {
		ddopts = append(ddopts, tracer.StartTime(ssConfig.Timestamp()))
	}
	if k := ssConfig.SpanKind(); k != 0 {
		ddopts = append(ddopts, tracer.Tag(ext.SpanKind, k.String()))
	}
	telemetry.Count(telemetry.NamespaceTracers, "spans_created", telemetryTags).Submit(1.0)
	var cfg tracer.StartSpanConfig
	cfg.Tags = make(map[string]interface{})
	if opts, ok := spanOptionsFromContext(ctx); ok {
		ddopts = append(ddopts, opts...)
		for _, o := range opts {
			o(&cfg)
		}
	}
	for _, attr := range ssConfig.Attributes() {
		k := string(attr.Key)
		if _, ok := cfg.Tags[k]; ok {
			continue
		}
		cfg.Tags[k] = attr.Value.AsInterface()
	}
	// Add provide OTel Span Links to the underlying Datadog span.
	if len(ssConfig.Links()) > 0 {
		links := make([]tracer.SpanLink, 0, len(ssConfig.Links()))
		for _, link := range ssConfig.Links() {
			ctx := otelCtxToDDCtx{link.SpanContext}
			attrs := make(map[string]string, len(link.Attributes))
			for _, attr := range link.Attributes {
				attrs[string(attr.Key)] = attr.Value.Emit()
			}
			links = append(links, tracer.SpanLink{
				TraceID:     ctx.TraceIDLower(),
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

	// Get the current OpenTelemetry baggage.
	otelBag := otelbaggage.FromContext(ctx)
	// Get the ddtrace baggage as a map[string]string.
	ddBag := baggage.All(ctx)

	// Merge the two baggage maps.
	// If there are conflicts, the OpenTelemetry baggage wins.
	var mergedBag otelbaggage.Baggage
	switch {
	case len(ddBag) == 0 && otelBag.Len() > 0:
		// Only OpenTelemetry baggage exists.
		mergedBag = otelBag
	case otelBag.Len() == 0 && len(ddBag) > 0:
		// Only Datadog baggage exists; convert it.
		var members []otelbaggage.Member
		for key, value := range ddBag {
			member, _ := otelbaggage.NewMember(key, value)
			members = append(members, member)
		}
		mergedBag, _ = otelbaggage.New(members...)
	case len(ddBag) > 0 && otelBag.Len() > 0:
		// Both baggage maps exist.
		// Merge the smaller one into the larger one; OpenTelemetry baggage wins on conflicts.
		if len(ddBag) <= otelBag.Len() {
			// ddBag is smaller: start with otelBag and add missing ddBag items.
			members := otelBag.Members()
			otelKeys := make(map[string]struct{}, otelBag.Len())
			for _, m := range otelBag.Members() {
				otelKeys[m.Key()] = struct{}{}
			}
			for key, value := range ddBag {
				if _, exists := otelKeys[key]; !exists {
					member, _ := otelbaggage.NewMember(key, value)
					members = append(members, member)
				}
			}
			mergedBag, _ = otelbaggage.New(members...)
		} else {
			// otelBag is smaller: start with ddBag converted to members, then override with otelBag.
			mergedMap := make(map[string]otelbaggage.Member, len(ddBag)+otelBag.Len())
			for key, value := range ddBag {
				member, _ := otelbaggage.NewMember(key, value)
				mergedMap[key] = member
			}
			for _, m := range otelBag.Members() {
				mergedMap[m.Key()] = m // otelBag value wins on conflict
			}
			var members []otelbaggage.Member
			for _, member := range mergedMap {
				members = append(members, member)
			}
			mergedBag, _ = otelbaggage.New(members...)
		}
	}
	if mergedBag.Len() > 0 {
		for _, m := range mergedBag.Members() {
			s.SetBaggageItem(m.Key(), m.Value())
			ctx = baggage.Set(ctx, m.Key(), m.Value())
		}
		ctx = otelbaggage.ContextWithBaggage(ctx, mergedBag)
	}

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

func (c *otelCtxToDDCtx) TraceID() string {
	id := c.oc.TraceID()
	return hex.EncodeToString(id[:])
}

func (c *otelCtxToDDCtx) TraceIDUpper() uint64 {
	id := c.oc.TraceID()
	return binary.BigEndian.Uint64(id[:8])
}

func (c *otelCtxToDDCtx) TraceIDBytes() [16]byte {
	return c.oc.TraceID()
}

func (c *otelCtxToDDCtx) TraceIDLower() uint64 {
	tid := c.oc.TraceID()
	return binary.BigEndian.Uint64(tid[8:])
}

func (c *otelCtxToDDCtx) SpanID() uint64 {
	id := c.oc.SpanID()
	return binary.BigEndian.Uint64(id[:])
}

func (c *otelCtxToDDCtx) ForeachBaggageItem(_ func(k, v string) bool) {}

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package opentelemetry

import (
	"context"
	"encoding/binary"
	"encoding/hex"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/baggage"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"

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

	// Merge baggage from otel and dd and update the context.
	if mergedBag, err := mergeBaggageFromContext(ctx); err == nil && mergedBag.Len() > 0 {
		for _, m := range mergedBag.Members() {
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

// mergeBaggageFromContext consolidates baggage from OpenTelemetry and Datadog sources.
func mergeBaggageFromContext(ctx context.Context) (otelbaggage.Baggage, error) {
	otelBag := otelbaggage.FromContext(ctx)
	ddBag := baggage.All(ctx)
	lenDDBag := len(ddBag)
	lenOtelBag := otelBag.Len()

	switch {
	case lenDDBag == 0 && lenOtelBag > 0:
		return otelBag, nil
	case lenOtelBag == 0 && lenDDBag > 0:
		return convertDDBaggage(ddBag)
	case lenDDBag > 0 && lenOtelBag > 0:
		if lenDDBag <= lenOtelBag {
			return mergeDDBagIntoOtel(otelBag, ddBag)
		}
		return mergeOtelBagIntoDD(otelBag, ddBag)
	}
	emptyBag, err := otelbaggage.New()
	if err != nil {
		return otelbaggage.Baggage{}, err
	}
	return emptyBag, nil
}

// convertDDBaggage converts a Datadog baggage map into an OpenTelemetry baggage.
func convertDDBaggage(ddBag map[string]string) (otelbaggage.Baggage, error) {
	var members []otelbaggage.Member
	for key, value := range ddBag {
		member, err := otelbaggage.NewMember(key, value)
		if err != nil {
			return otelbaggage.Baggage{}, err
		}
		members = append(members, member)
	}
	return otelbaggage.New(members...)
}

// mergeDDBagIntoOtel merges Datadog baggage into the existing OpenTelemetry baggage,
// adding only keys that are missing in the OpenTelemetry baggage.
func mergeDDBagIntoOtel(otelBag otelbaggage.Baggage, ddBag map[string]string) (otelbaggage.Baggage, error) {
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
	return otelbaggage.New(members...)
}

// mergeOtelBagIntoDD merges OpenTelemetry baggage into a Datadog baggage map,
// overriding keys with the OpenTelemetry values in case of conflict.
func mergeOtelBagIntoDD(otelBag otelbaggage.Baggage, ddBag map[string]string) (otelbaggage.Baggage, error) {
	mergedMap := make(map[string]otelbaggage.Member, len(ddBag)+otelBag.Len())
	for key, value := range ddBag {
		member, _ := otelbaggage.NewMember(key, value)
		mergedMap[key] = member
	}
	for _, m := range otelBag.Members() {
		mergedMap[m.Key()] = m
	}
	var members []otelbaggage.Member
	for _, member := range mergedMap {
		members = append(members, member)
	}
	return otelbaggage.New(members...)
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

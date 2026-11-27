// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package opentelemetry

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"log/slog"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/baggage"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	telemetrylog "github.com/DataDog/dd-trace-go/v2/internal/telemetry/log"

	"go.opentelemetry.io/otel/attribute"
	otelbaggage "go.opentelemetry.io/otel/baggage"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

var _ oteltrace.Tracer = (*oteltracer)(nil)

var (
	telemetryTags = []string{"integration_name:otel"}

	// spanKindTagValues caches pre-boxed any values for each standard OTel SpanKind
	// so Start sets ext.SpanKind directly on the span without forcing tracer.StartSpan
	// to allocate an opts.Tags map or boxing the string on every span.
	spanKindTagValues = [...]any{
		oteltrace.SpanKindInternal: oteltrace.SpanKindInternal.String(),
		oteltrace.SpanKindServer:   oteltrace.SpanKindServer.String(),
		oteltrace.SpanKindClient:   oteltrace.SpanKindClient.String(),
		oteltrace.SpanKindProducer: oteltrace.SpanKindProducer.String(),
		oteltrace.SpanKindConsumer: oteltrace.SpanKindConsumer.String(),
	}
)

type oteltracer struct {
	noop.Tracer // https://pkg.go.dev/go.opentelemetry.io/otel/trace#hdr-API_Implementations
	provider    *TracerProvider
	DD          tracer.Tracer
	// spansCreated caches the telemetry counter handle so Start avoids a map
	// lookup and key allocation on every span.
	spansCreated telemetry.MetricHandle
	// otelSemanticsEnabled is resolved once when the provider is created, so spans
	// don't read the global configuration on every SetAttributes call.
	otelSemanticsEnabled bool
}

func (t *oteltracer) Start(ctx context.Context, spanName string, opts ...oteltrace.SpanStartOption) (context.Context, oteltrace.Span) {
	if datadogTracerInactive() {
		// The Datadog tracer does not record. Return a non-recording span that keeps the parent
		// span context, as the OpenTelemetry no-op tracer does.
		return noop.NewTracerProvider().Tracer("").Start(ctx, spanName, opts...)
	}
	var (
		newRoot  bool
		spanKind oteltrace.SpanKind
		ssConfig oteltrace.SpanConfig
	)
	if len(opts) > 0 {
		ssConfig = oteltrace.NewSpanStartConfig(opts...)
		newRoot = ssConfig.NewRoot()
		spanKind = ssConfig.SpanKind()
	}

	var ddoptsBuf [4]tracer.StartSpanOption
	ddopts := ddoptsBuf[:0]

	startOpts, hasStartOpts := spanOptionsFromContext(ctx)
	ddParent, hasDD := tracer.SpanFromContext(ctx)
	startCtx := ctx
	if newRoot {
		if hasDD {
			startCtx = tracer.ContextWithSpan(ctx, nil)
		}
	} else if hasDD {
		if hasStartOpts {
			// Pass the Datadog parent before startOpts so an explicit ChildOf in
			// startOpts can override it, and detach the ambient span from startCtx.
			ddopts = append(ddopts, tracer.ChildOf(ddParent.Context()))
			startCtx = tracer.ContextWithSpan(ctx, nil)
		}
		// Otherwise StartSpanFromContext inherits ddParent from startCtx directly.
	} else if sctx := oteltrace.SpanFromContext(ctx).SpanContext(); sctx.IsValid() {
		// if the span doesn't originate from the Datadog tracer,
		// use SpanContextW3C implementation struct to pass span context information
		ddopts = append(ddopts, tracer.ChildOf(tracer.FromGenericCtx(&otelCtxToDDCtx{sctx})))
	}

	if len(opts) > 0 {
		if ts := ssConfig.Timestamp(); !ts.IsZero() {
			ddopts = append(ddopts, tracer.StartTime(ts))
		}
	}

	if t.spansCreated != nil {
		t.spansCreated.Submit(1.0)
	} else {
		telemetry.Count(telemetry.NamespaceTracers, "spans_created", telemetryTags).Submit(1.0)
	}

	var startOptTags map[string]any
	if hasStartOpts {
		var cfg tracer.StartSpanConfig
		cfg.Tags = make(map[string]any)
		ddopts = append(ddopts, startOpts...)
		for _, o := range startOpts {
			o(&cfg)
		}
		startOptTags = cfg.Tags
	}
	if len(opts) > 0 {
		// Add provided OTel Span Links to the underlying Datadog span.
		if ssLinks := ssConfig.Links(); len(ssLinks) > 0 {
			links := make([]tracer.SpanLink, 0, len(ssLinks))
			for _, link := range ssLinks {
				lctx := otelCtxToDDCtx{link.SpanContext}
				lattrs := make(map[string]string, len(link.Attributes))
				for _, attr := range link.Attributes {
					lattrs[string(attr.Key)] = attr.Value.Emit()
				}
				links = append(links, tracer.SpanLink{
					TraceID:     lctx.TraceIDLower(),
					TraceIDHigh: lctx.TraceIDUpper(),
					SpanID:      lctx.SpanID(),
					Tracestate:  link.SpanContext.TraceState().String(),
					Attributes:  lattrs,
					// To distinguish between "not sampled" and "not set", Datadog
					// will rely on the highest bit being set. The OTel API doesn't
					// differentiate this, so we will just always mark it as set.
					Flags: uint32(link.SpanContext.TraceFlags()) | (1 << 31),
				})
			}
			ddopts = append(ddopts, tracer.WithSpanLinks(links))
		}
	}
	// StartSpanFromContext keeps the application's pprof labels on the goroutine
	// and attaches this span's profiling labels to the returned context.
	// It also initializes span.resource to spanName by default.
	s, ctx := tracer.StartSpanFromContext(startCtx, spanName, ddopts...)

	// Merge baggage from otel and dd only when at least one side has entries.
	ctx = syncBaggageOnContext(ctx)

	var flags spanFlags
	if t.otelSemanticsEnabled {
		flags |= flagOTelSemantics
	}
	sp := &span{
		DD:         s,
		ddCtx:      s.Context(),
		oteltracer: t,
		spanKind:   spanKind,
		flags:      flags,
	}
	for k, v := range startOptTags {
		sp.recordAnyAttrMeta(k, v)
	}
	if spanKind != 0 {
		if _, ok := startOptTags[ext.SpanKind]; !ok {
			if int(spanKind) > 0 && int(spanKind) < len(spanKindTagValues) {
				s.SetTag(ext.SpanKind, spanKindTagValues[spanKind])
			} else {
				s.SetTag(ext.SpanKind, spanKind.String())
			}
		}
	}
	if len(opts) > 0 {
		for _, attr := range ssConfig.Attributes() {
			k := string(attr.Key)
			if _, ok := startOptTags[k]; ok {
				continue
			}
			if !t.otelSemanticsEnabled && (k == "operation.name" || k == ext.SpanName) {
				if attr.Value.Type() == attribute.STRING {
					sp.opName = strings.ToLower(attr.Value.AsString())
					sp.flags &^= flagOpNameNonString
				} else if k == ext.SpanName {
					sp.opName = ""
					sp.flags |= flagOpNameNonString
				}
			}
			sp.recordAttrMeta(k, attr.Value)
			s.SetTag(k, attr.Value.AsInterface())
		}
	}
	os := oteltrace.Span(sp)
	if hasStartOpts {
		// Erase the start span options from the context to prevent them from being propagated to children.
		ctx = context.WithValue(ctx, startOptsKey, nil)
	}
	// Wrap the span in the OpenTelemetry context to propagate span context values.
	// StartSpanFromContext already put the Datadog span on the context.
	ctx = oteltrace.ContextWithSpan(ctx, os)
	return ctx, os
}

// syncBaggageOnContext merges Datadog and OpenTelemetry baggage on ctx when either
// is non-empty, avoiding context allocations in the common empty-baggage case.
func syncBaggageOnContext(ctx context.Context) context.Context {
	otelBag := otelbaggage.FromContext(ctx)
	ddExisting := baggage.All(ctx)
	if otelBag.Len() == 0 && len(ddExisting) == 0 {
		return ctx
	}
	otelChanged := false
	for key, value := range ddExisting {
		if otelBag.Member(key).Value() == "" {
			member, err := otelbaggage.NewMemberRaw(key, value)
			if err == nil {
				b, err := otelBag.SetMember(member)
				if err == nil {
					otelBag = b
					otelChanged = true
				} else {
					telemetrylog.Debug("Error adding baggage member; dropping", slog.String("key", key))
				}
			}
		}
	}
	members := otelBag.Members()
	if len(members) > 0 {
		ddChanged := false
		for _, m := range members {
			if existing, ok := ddExisting[m.Key()]; !ok || existing != m.Value() {
				ddChanged = true
				break
			}
		}
		if ddChanged {
			ddBag := make(map[string]string, len(members))
			for _, m := range members {
				ddBag[m.Key()] = m.Value()
			}
			ctx = baggage.SetAll(ctx, ddBag)
		}
	}
	if otelChanged {
		ctx = otelbaggage.ContextWithBaggage(ctx, otelBag)
	}
	return ctx
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

// Tracestate returns the W3C tracestate of the span context, so that the
// Datadog tracer can restore the state carried in its dd= member.
func (c *otelCtxToDDCtx) Tracestate() string {
	return c.oc.TraceState().String()
}

// SamplingDecision returns the sampling decision associated with this span context.
// According to the OpenTelemetry specification, the sampling decision is made when the span is started.
// Therefore, not having the Sampled TraceFlag set means that the trace should be dropped.
// - https://github.com/open-telemetry/opentelemetry-go/blob/main/sdk/trace/tracer.go#L109
// - https://github.com/open-telemetry/opentelemetry-go/blob/main/sdk/trace/tracer.go#L126
func (c *otelCtxToDDCtx) SamplingDecision() uint32 {
	if c.oc.IsSampled() {
		return 2 // decisionKeep
	}
	return 1 // decisionDrop
}

// Priority returns the sampling priority associated with this span context.
// According to the OpenTelemetry specification, the sampling decision is made when the span is started.
// Therefore, not having the Sampled TraceFlag set means that the trace should be dropped.
// - https://github.com/open-telemetry/opentelemetry-go/blob/main/sdk/trace/tracer.go#L109
// - https://github.com/open-telemetry/opentelemetry-go/blob/main/sdk/trace/tracer.go#L126
func (c *otelCtxToDDCtx) Priority() *float64 {
	if c.oc.IsSampled() {
		p := float64(ext.PriorityAutoKeep)
		return &p
	}
	p := float64(ext.PriorityAutoReject)
	return &p
}

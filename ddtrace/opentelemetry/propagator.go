// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package opentelemetry

import (
	"context"

	"go.opentelemetry.io/otel/propagation"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// DatadogPropagator implements propagation.TextMapPropagator using the Datadog
// trace propagation format. It delegates to the global Datadog tracer for
// extraction and injection, so it respects all DD_TRACE_PROPAGATION_* env vars,
// including DD_TRACE_PROPAGATION_BEHAVIOR_EXTRACT=restart.
//
// Other Datadog tracer integrations (Java, Ruby, .NET) automatically register
// a Datadog-aware propagator at startup. In Go, include DatadogPropagator
// explicitly in your composite propagator alongside TraceContext and Baggage
// to get the same behavior:
//
//	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
//	    opentelemetry.DatadogPropagator{},
//	    propagation.TraceContext{},
//	    propagation.Baggage{},
//	))
type DatadogPropagator struct{}

var _ propagation.TextMapPropagator = DatadogPropagator{}

// Fields returns the Datadog-specific HTTP header keys that Inject writes.
func (DatadogPropagator) Fields() []string {
	return []string{
		tracer.DefaultTraceIDHeader,
		tracer.DefaultParentIDHeader,
		tracer.DefaultPriorityHeader,
		"x-datadog-origin",
		"x-datadog-tags",
	}
}

// Extract reads Datadog trace headers from carrier and returns a context
// augmented with DD tracer start options. This ensures that env var config
// like DD_TRACE_PROPAGATION_BEHAVIOR_EXTRACT=restart is applied when the next
// span is started via the OTel bridge (ddotel.NewTracerProvider).
//
// The span links produced by the restart behavior are stored in the context via
// ContextWithStartOptions and are picked up by the bridge when the span is
// created. Both ChildOf and WithSpanLinks must be passed because ChildOf alone
// does not transfer span links from a baggageOnly SpanContext.
func (DatadogPropagator) Extract(ctx context.Context, carrier propagation.TextMapCarrier) context.Context {
	ddSpanCtx, err := tracer.Extract(otelCarrierAdapter{carrier})
	if err != nil || ddSpanCtx == nil {
		return ctx
	}
	opts := []tracer.StartSpanOption{tracer.ChildOf(ddSpanCtx)}
	if links := ddSpanCtx.SpanLinks(); len(links) > 0 {
		opts = append(opts, tracer.WithSpanLinks(links))
	}
	return ContextWithStartOptions(ctx, opts...)
}

// Inject writes the active Datadog span context from ctx into carrier using
// the Datadog propagation format.
func (DatadogPropagator) Inject(ctx context.Context, carrier propagation.TextMapCarrier) {
	span, ok := tracer.SpanFromContext(ctx)
	if !ok {
		return
	}
	// Errors from Inject are silently ignored to match the OTel propagator contract.
	tracer.Inject(span.Context(), otelCarrierAdapter{carrier}) //nolint:errcheck
}

// otelCarrierAdapter wraps an OTel TextMapCarrier so it can be passed to the
// DD tracer's Extract and Inject, which expect TextMapReader / TextMapWriter.
type otelCarrierAdapter struct {
	carrier propagation.TextMapCarrier
}

// ForeachKey implements tracer.TextMapReader.
func (a otelCarrierAdapter) ForeachKey(handler func(key, val string) error) error {
	for _, k := range a.carrier.Keys() {
		if err := handler(k, a.carrier.Get(k)); err != nil {
			return err
		}
	}
	return nil
}

// Set implements tracer.TextMapWriter.
func (a otelCarrierAdapter) Set(key, val string) {
	a.carrier.Set(key, val)
}

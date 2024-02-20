// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"context"

	v2mock "github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	v2 "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
)

// ContextWithSpan returns a copy of the given context which includes the span s.
func ContextWithSpan(ctx context.Context, s Span) context.Context {
	var sp *v2.Span
	if s == nil {
		sp = nil
	} else {
		sp = s.(internal.SpanV2Adapter).Span
	}
	return v2.ContextWithSpan(ctx, sp)
}

// SpanFromContext returns the span contained in the given context. A second return
// value indicates if a span was found in the context. If no span is found, a no-op
// span is returned.
func SpanFromContext(ctx context.Context) (Span, bool) {
	span, ok := v2.SpanFromContext(ctx)
	if !ok {
		return &internal.NoopSpan{}, false
	}
	if mocktracer.IsActive() {
		return mocktracer.MockspanV2Adapter{Span: v2mock.MockSpan(span)}, true
	}
	return internal.SpanV2Adapter{Span: span}, true
}

// StartSpanFromContext returns a new span with the given operation name and options. If a span
// is found in the context, it will be used as the parent of the resulting span. If the ChildOf
// option is passed, it will only be used as the parent if there is no span found in `ctx`.
func StartSpanFromContext(ctx context.Context, operationName string, opts ...StartSpanOption) (Span, context.Context) {
	cfg := internal.BuildStartSpanConfigV2(opts...)
	span, ctx := v2.StartSpanFromContext(ctx, operationName, v2.WithStartSpanConfig(cfg))
	return internal.SpanV2Adapter{Span: span}, ctx
}

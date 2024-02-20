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
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, internal.ActiveSpanKey, s)
}

// SpanFromContext returns the span contained in the given context. A second return
// value indicates if a span was found in the context. If no span is found, a no-op
// span is returned.
func SpanFromContext(ctx context.Context) (Span, bool) {
	if ctx == nil {
		return internal.NoopSpan{}, false
	}
	v := ctx.Value(internal.ActiveSpanKey)
	switch v.(type) {
	case internal.SpanV2Adapter:
		sa := v.(internal.SpanV2Adapter)
		if mocktracer.IsActive() {
			return mocktracer.MockspanV2Adapter{Span: v2mock.MockSpan(sa.Span)}, true
		}
		return sa, true
	case Span:
		return v.(Span), true
	default:
		return internal.NoopSpan{}, false
	}
}

// StartSpanFromContext returns a new span with the given operation name and options. If a span
// is found in the context, it will be used as the parent of the resulting span. If the ChildOf
// option is passed, it will only be used as the parent if there is no span found in `ctx`.
func StartSpanFromContext(ctx context.Context, operationName string, opts ...StartSpanOption) (Span, context.Context) {
	cfg := internal.BuildStartSpanConfigV2(opts...)
	span, ctx := v2.StartSpanFromContext(ctx, operationName, v2.WithStartSpanConfig(cfg))
	sa := internal.SpanV2Adapter{Span: span}
	ctx = ContextWithSpan(ctx, sa)
	return sa, ctx
}

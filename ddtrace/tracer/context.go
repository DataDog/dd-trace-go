// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"context"

	v2mock "github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	v2 "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
)

// ContextWithSpan returns a copy of the given context which includes the span s.
func ContextWithSpan(ctx context.Context, s ddtrace.Span) context.Context {
	switch s := s.(type) {
	case internal.SpanV2Adapter:
		return v2.ContextWithSpan(ctx, s.Span)
	case mocktracer.MockspanV2Adapter:
		return v2.ContextWithSpan(ctx, s.Span.Unwrap())
	case internal.NoopSpan:
		return v2.ContextWithSpan(ctx, nil)
	}
	// TODO: remove this case once we remove the v1 tracer
	return ctx
}

// SpanFromContext returns the span contained in the given context. A second return
// value indicates if a span was found in the context. If no span is found, a no-op
// span is returned.
func SpanFromContext(ctx context.Context) (ddtrace.Span, bool) {
	s, ok := v2.SpanFromContext(ctx)
	if !ok {
		return internal.NoopSpan{}, false
	}
	if mocktracer.IsActive() {
		return mocktracer.MockspanV2Adapter{Span: v2mock.MockSpan(s)}, true
	}
	return internal.WrapSpan(s), true
}

// StartSpanFromContext returns a new span with the given operation name and options. If a span
// is found in the context, it will be used as the parent of the resulting span. If the ChildOf
// option is passed, it will only be used as the parent if there is no span found in `ctx`.
func StartSpanFromContext(ctx context.Context, operationName string, opts ...ddtrace.StartSpanOption) (ddtrace.Span, context.Context) {
	span, ctx := v2.StartSpanFromContext(ctx, operationName, internal.ApplyV1Options(opts...))
	var s ddtrace.Span
	if mocktracer.IsActive() {
		s = mocktracer.MockspanV2Adapter{Span: v2mock.MockSpan(span)}
	} else {
		s = internal.WrapSpan(span)
	}
	return s, ctx
}

// SpanContextV2Adapter is an adapter for the v2 SpanContext type.
// This is not intended for direct use, but rather to be used by the
// contribs.
type SpanContextV2Adapter = internal.SpanContextV2Adapter

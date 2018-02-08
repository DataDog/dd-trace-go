package tracer

import (
	"context"

	"github.com/DataDog/dd-trace-go/dd"
)

type contextKey struct{}

var activeSpanKey = contextKey{}

// ContextWithSpan returns a copy of the given context which includes the span.
func ContextWithSpan(ctx context.Context, s Span) context.Context {
	return context.WithValue(ctx, activeSpanKey, s)
}

// SpanFromContext returns the span contained in the given context. If no span is
// found, it returns nil.
func SpanFromContext(ctx context.Context) Span {
	v := ctx.Value(activeSpanKey)
	if s, ok := v.(dd.Span); ok {
		return s
	}
	return nil
}

// StartSpanFromContext returns a new span with the given operation name and options. If a span
// is found in the context, it will be used as the parent of the resulting span.
func StartSpanFromContext(ctx context.Context, operationName string, opts ...StartSpanOption) (Span, context.Context) {
	if s := SpanFromContext(ctx); s != nil {
		opts = append(opts, ChildOf(s.Context()))
	}
	s := StartSpan(operationName, opts...)
	return s, ContextWithSpan(ctx, s)
}

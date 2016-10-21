package tracer

import (
	"context"
)

type datadogContextKey struct{}

var spanKey = datadogContextKey{}

// ContextWithSpan will return a new context that includes the given span.
func ContextWithSpan(ctx context.Context, span *Span) context.Context {
	return context.WithValue(ctx, spanKey, span)
}

// ContextWithNewChildSpan will create a new child span, and returns the
// created span and a new context that includes the created span.
func ContextWithNewChildSpan(name string, ctx context.Context) (*Span, context.Context) {
	span := NewChildSpanFromContext(name, ctx)
	return span, context.WithValue(ctx, spanKey, span)
}

// SpanFromContext returns the stored *Span from the Context if it's available.
// This helper returns also the ok value that is true if the span is present.
func SpanFromContext(ctx context.Context) (*Span, bool) {
	if ctx == nil {
		return nil, false
	}
	span, ok := ctx.Value(spanKey).(*Span)
	return span, ok
}

// SpanFromContextDefault returns the stored *Span from the Context. If not, it
// will return an empty span that will do nothing.
func SpanFromContextDefault(ctx context.Context) *Span {

	// FIXME[matt] is it better to return a singleton empty span?
	if ctx == nil {
		return &Span{}
	}

	span, ok := SpanFromContext(ctx)
	if !ok {
		return &Span{}
	}
	return span
}

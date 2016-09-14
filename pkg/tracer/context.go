package tracer

import (
	"golang.org/x/net/context"
)

type datadogContextKey struct{}

var datadogActiveSpanKey = datadogContextKey{}

// ContextWithSpan populates the given Context with a Span using an internal
// datadogActiveSpanKey. This method is a good helper to pass parent spans
// in a new function call, to simplify the creation of a child span.
func ContextWithSpan(ctx context.Context, span *Span) context.Context {
	return context.WithValue(ctx, datadogActiveSpanKey, span)
}

// SpanFromContext returns the stored *Span from the Context if it's available.
// This helper returns also the ok value that is true if the span is present.
func SpanFromContext(ctx context.Context) (*Span, bool) {
	// TODO[manu]: split the return just for clarity of the review; one-liner later
	span, ok := ctx.Value(datadogActiveSpanKey).(*Span)
	return span, ok
}

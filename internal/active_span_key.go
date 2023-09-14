package internal

type contextKey struct{}

// ActiveSpanKey is used to set tracer context on a context.Context objects with a unique key
var ActiveSpanKey = contextKey{}

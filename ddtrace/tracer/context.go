// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"context"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/options"
	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion"
)

// ContextWithSpan returns a copy of the given context which includes the span s.
func ContextWithSpan(ctx context.Context, s *Span) context.Context {
	return orchestrion.CtxWithValue(ctx, internal.ActiveSpanKey, s)
}

// SpanFromContext returns the span contained in the given context. A second return
// value indicates if a span was found in the context. If no span is found, a no-op
// span is returned.
func SpanFromContext(ctx context.Context) (*Span, bool) {
	if ctx == nil {
		return nil, false
	}
	v := orchestrion.WrapContext(ctx).Value(internal.ActiveSpanKey)
	if s, ok := v.(*Span); ok {
		// We may have a nil *Span wrapped in an interface in the GLS context stack,
		// in which case we need to act a if there was nothing (for else we'll
		// forcefully un-do a [ChildOf] option if one was passed).
		return s, s != nil
	}
	return nil, false
}

// StartSpanFromContext returns a new span with the given operation name and options. If a span
// is found in the context, it will be used as the parent of the resulting span. If the ChildOf
// option is passed, it will only be used as the parent if there is no span found in `ctx`.
func StartSpanFromContext(ctx context.Context, operationName string, opts ...StartSpanOption) (*Span, context.Context) {
	// copy opts in case the caller reuses the slice in parallel
	// we will add at least 1, at most 2 items
	optsLocal := options.Expand(opts, 0, 2)
	if ctx == nil {
		// default to context.Background() to avoid panics on Go >= 1.15
		ctx = context.Background()
	} else if s, ok := SpanFromContext(ctx); ok {
		optsLocal = append(optsLocal, ChildOf(s.Context()))
	}
	optsLocal = append(optsLocal, withContext(ctx))
	s := StartSpan(operationName, optsLocal...)
	if s != nil && s.pprofCtxActive != nil {
		ctx = s.pprofCtxActive
	}
	return s, ContextWithSpan(ctx, s)
}

// BaggageContext represents a context that can carry baggage items across process boundaries.
// It handles both W3C Baggage (the standard) and OpenTracing baggage (legacy, to be deprecated).
type BaggageContext interface {
	context.Context

	// W3C Baggage methods (the future standard)
	GetBaggage(key string) (value string, ok bool)
	SetBaggage(key, value string) BaggageContext
	AllBaggage() map[string]string
	ForeachBaggage(handler func(key, value string) bool)
	ClearBaggage() BaggageContext

	// OpenTracing Baggage methods (legacy - will be deprecated)
	GetOTBaggage(key string) string
	SetOTBaggage(key, value string) BaggageContext
	ForeachOTBaggage(handler func(key, value string) bool)
	HasBaggage() bool

	// Context operations
	WithParent(parent context.Context) BaggageContext
}

// TraceContext represents trace-level propagation information.
// This contains only the information that belongs to the trace as a whole.
type TraceContext interface {
	TraceID() string
	TraceIDBytes() [16]byte
	TraceIDLower() uint64
	TraceIDUpper() uint64
	SamplingPriority() (priority int, ok bool)
	Origin() string

	// Validation
	IsValid() bool // Returns true if TraceID is non-zero
}

// PropagationContext composes trace and baggage contexts for clean separation of concerns.
// This eliminates the need for special "baggage-only" flags and convoluted control flow.
type PropagationContext interface {
	context.Context

	// Trace context (can be nil if no trace propagation)
	Trace() TraceContext

	// Baggage context (can be nil if no baggage)
	Baggage() BaggageContext

	// Convenience methods
	HasTrace() bool
	HasBaggage() bool

	// Create new contexts with modifications
	WithTrace(trace TraceContext) PropagationContext
	WithBaggage(baggage BaggageContext) PropagationContext
	WithParent(parent context.Context) PropagationContext
}

// PropagationContextKey is the key used to store PropagationContext in context.Context
type PropagationContextKey struct{}

// ExtractPropagationContext extracts a PropagationContext from a context.Context if present.
func ExtractPropagationContext(ctx context.Context) (PropagationContext, bool) {
	if ctx == nil {
		return nil, false
	}
	propCtx, ok := ctx.Value(PropagationContextKey{}).(PropagationContext)
	return propCtx, ok
}

// WithPropagationContext stores a PropagationContext in a context.Context.
func WithPropagationContext(parent context.Context, propCtx PropagationContext) context.Context {
	if propCtx == nil {
		return parent
	}
	return context.WithValue(parent, PropagationContextKey{}, propCtx)
}

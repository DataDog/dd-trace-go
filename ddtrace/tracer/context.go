// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"context"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/options"
	"github.com/DataDog/dd-trace-go/v2/internal"
	illmobs "github.com/DataDog/dd-trace-go/v2/internal/llmobs"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// activeSpanContextKey is a context key for the snapshotted SpanContext.
// When a Span is stored in a Go context via ContextWithSpan, we also snapshot
// its SpanContext so that StartSpanFromContext reads the original parent
// identity (traceID, spanID, sampling priority) even after the *Span is
// recycled and its s.context field replaced. This does NOT protect the
// underlying trace from being finished or flushed; callers must ensure the
// parent's trace lifetime exceeds child span creation.
type activeSpanContextKey struct{}

// ContextWithSpan returns a copy of the given context which includes the span s.
// If ctx is nil, a new background context is created to avoid panicking.
func ContextWithSpan(ctx context.Context, s *Span) context.Context {
	if ctx == nil {
		log.Warn("ContextWithSpan: received nil context, falling back to context.Background()")
		ctx = context.Background()
	}
	// Plain context.WithValue. When built with orchestrion, three aspects in
	// ddtrace/tracer/orchestrion.yml extend the span's GLS lifecycle:
	//   - "Span GLS fields": adds two woven fields to Span — __dd_glsPop
	//     (GLSPopperCell, an atomic pointer to the goroutine-scoped popper) and
	//     __dd_glsReclaimable (atomic.Bool, set on finish so cross-goroutine GLS
	//     entries can be lazily reclaimed on the next push).
	//   - "Span ContextWithSpan GLS push": prepends before this line:
	//       orchestrion.GLSActivate(nil, ActiveSpanKey, s, &s.__dd_glsPop)
	//     which pushes s onto the goroutine-local stack and records a goroutine-
	//     scoped popper in __dd_glsPop (first push wins; no-op when disabled).
	//   - "Span Finish GLS deactivate": prepends at the top of Span.Finish:
	//       orchestrion.GLSDeactivate(&s.__dd_glsReclaimable, &s.__dd_glsPop)
	//     which pops the GLS entry exactly once, only on the goroutine that pushed.
	// SpanFromContext is extended analogously ("Span SpanFromContext GLS read").
	// Without orchestrion there is no GLS; this is a plain context.WithValue.
	newCtx := context.WithValue(ctx, internal.ActiveSpanKey, s)
	if s != nil {
		// Snapshot the SpanContext so it survives span pool recycling.
		newCtx = context.WithValue(newCtx, activeSpanContextKey{}, s.Context())
	}
	return contextWithPropagatedLLMSpan(newCtx, s)
}

func contextWithPropagatedLLMSpan(ctx context.Context, s *Span) context.Context {
	if s == nil {
		return ctx
	}
	// if there is a propagated llm span already just skip
	if _, ok := illmobs.PropagatedLLMSpanFromContext(ctx); ok {
		return ctx
	}
	propagatedLLMObs := propagatedLLMSpanFromTags(s)
	if propagatedLLMObs.SpanID == "" || propagatedLLMObs.TraceID == "" {
		return ctx
	}
	return illmobs.ContextWithPropagatedLLMSpan(ctx, propagatedLLMObs)
}

// propagatedLLMSpanFromTags extracts LLMObs propagation information from the trace propagating tags.
// This is used during distributed tracing to set the correct parent span for the current span.
func propagatedLLMSpanFromTags(s *Span) *illmobs.PropagatedLLMSpan {
	propagatedLLMObs := &illmobs.PropagatedLLMSpan{}
	if s.context == nil || s.context.trace == nil {
		return propagatedLLMObs
	}
	if parentID := s.context.trace.propagatingTag(keyPropagatedLLMObsParentID); parentID != "" {
		propagatedLLMObs.SpanID = parentID
	}
	if mlApp := s.context.trace.propagatingTag(keyPropagatedLLMObsMLAPP); mlApp != "" {
		propagatedLLMObs.MLApp = mlApp
	}
	if trID := s.context.trace.propagatingTag(keyPropagatedLLMObsTraceID); trID != "" {
		propagatedLLMObs.TraceID = trID
	}
	return propagatedLLMObs
}

// SpanFromContext returns the span contained in the given context. A second return
// value indicates if a span was found in the context. If no span is found, a no-op
// span is returned.
func SpanFromContext(ctx context.Context) (*Span, bool) {
	if ctx == nil {
		return nil, false
	}
	// Plain context lookup. Under orchestrion, "Span SpanFromContext GLS read"
	// (ddtrace/tracer/orchestrion.yml) prepends:
	//   ctx = orchestrion.WrapContext(ctx)
	// so ctx.Value also consults the goroutine-local stack as a fallback when the
	// explicit context chain carries no active span (e.g. un-instrumented callers).
	// Without orchestrion this is a bare ctx.Value.
	v := ctx.Value(internal.ActiveSpanKey)
	if s, ok := v.(*Span); ok {
		// We may have a nil *Span wrapped in an interface in the GLS context stack,
		// in which case we need to act as if there was nothing (otherwise we'll
		// forcefully un-do a [ChildOf] option if one was passed).
		if s == nil {
			return nil, false
		}
		return s, true
	}
	return nil, false
}

// StartSpanFromContext returns a new span with the given operation name and options. If a span
// is found in the context, it will be used as the parent of the resulting span. If the ChildOf
// option is passed, it will only be used as the parent if there is no span found in `ctx`.
// +checklocksignore — Initialization time, span just created by StartSpan, not yet shared.
func StartSpanFromContext(ctx context.Context, operationName string, opts ...StartSpanOption) (*Span, context.Context) {
	// copy opts in case the caller reuses the slice in parallel
	// we will add at least 1, at most 2 items
	optsLocal := options.Expand(opts, 0, 2)
	if ctx == nil {
		// default to context.Background() to avoid panics on Go >= 1.15
		ctx = context.Background()
	} else if sc, ok := ctx.Value(activeSpanContextKey{}).(*SpanContext); ok && sc != nil {
		// Prefer the snapshotted SpanContext to handle span pool recycling.
		optsLocal = append(optsLocal, ChildOf(sc))
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

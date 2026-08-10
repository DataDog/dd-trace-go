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
// ContextWithSpan(ctx, nil) clears this snapshot too, not just
// internal.ActiveSpanKey, so detaching from an ambient span also detaches
// from any snapshot inherited from an ancestor context.
type activeSpanContextKey struct{}

// ContextWithSpan returns a copy of the given context which includes the span s.
// If ctx is nil, a new background context is created to avoid panicking.
// Passing a nil span detaches ctx from any ambient span, including one
// inherited from an ancestor context, so a subsequent [StartSpanFromContext]
// starts a new root span — unless the caller also passes an explicit parent
// (e.g. ChildOf) to that call, which is honored as usual.
func ContextWithSpan(ctx context.Context, s *Span) context.Context {
	if ctx == nil {
		log.Warn("ContextWithSpan: received nil context, falling back to context.Background()")
		ctx = context.Background()
	}
	// Plain context.WithValue. When built with orchestrion, three aspects in
	// ddtrace/tracer/orchestrion.yml extend the span's GLS lifecycle:
	//   - "Span GLS fields": adds two woven fields to Span — __dd_glsPop
	//     (GLSPopperCell, an atomic pointer to the goroutine-scoped popper) and
	//     __dd_glsDone (GLSDoneCell, an atomic pointer to the liveness cell marked
	//     on finish so a cross-goroutine GLS entry is never handed out as the
	//     active span and is dropped by the next push). The cell is reached through
	//     a pointer rather than stored inline so the signal survives the span being
	//     recycled by the span pool.
	//   - "Span ContextWithSpan GLS push": prepends before this line:
	//       orchestrion.GLSActivate(nil, ActiveSpanKey, s, &s.__dd_glsPop, &s.__dd_glsDone)
	//     which pushes s onto the goroutine-local stack and records a goroutine-
	//     scoped popper in __dd_glsPop (first push wins; no-op when disabled).
	//   - "Span Finish GLS deactivate": prepends at the top of Span.Finish:
	//       orchestrion.GLSDeactivate(&s.__dd_glsDone, &s.__dd_glsPop)
	//     which pops the GLS entry exactly once, only on the goroutine that pushed.
	// SpanFromContext is extended analogously ("Span SpanFromContext GLS read").
	// Without orchestrion there is no GLS; this is a plain context.WithValue.
	newCtx := context.WithValue(ctx, internal.ActiveSpanKey, s)
	if s != nil {
		// Snapshot the SpanContext so it survives span pool recycling.
		newCtx = context.WithValue(newCtx, activeSpanContextKey{}, s.Context())
	} else if sc, ok := ctx.Value(activeSpanContextKey{}).(*SpanContext); ok && sc != nil {
		// Shadow a snapshot inherited from an ancestor context, otherwise
		// StartSpanFromContext would keep re-parenting onto it even though
		// ActiveSpanKey was just cleared above. Only an ancestor that actually
		// holds a snapshot needs shadowing: ContextWithSpan(ctx, nil) is on the
		// hot path whenever the tracer is disabled (StartSpan returns nil), so
		// paying an allocation to shadow nothing would be wasted on every span.
		newCtx = context.WithValue(newCtx, activeSpanContextKey{}, (*SpanContext)(nil))
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
	if sessionID := s.context.trace.propagatingTag(keyPropagatedLLMObsSessionID); sessionID != "" {
		propagatedLLMObs.SessionID = sessionID
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
// was placed in ctx by [ContextWithSpan], it is used as the parent of the resulting span even if
// the ChildOf option is passed. An active span discovered any other way — notably through
// Orchestrion's goroutine-local storage fallback — is only used as the parent when the caller
// did not pass a non-nil ChildOf.
//
// ChildOf(nil) counts as unset rather than as a request for a root span, because
// [StartSpanConfig.Parent] gives nil that meaning already and integrations pass the nil that
// [Extract] returns when propagation is disabled. Such a call still gets the discovered parent.
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
		// Reached only when the context chain carries no span snapshot, i.e. the
		// span came from somewhere other than ContextWithSpan. In practice that
		// is Orchestrion's GLS fallback, which is an inference about the current
		// scope rather than a parent the caller named, so it must not override an
		// explicit ChildOf. See childOfIfUnset.
		optsLocal = append(optsLocal, childOfIfUnset(s.Context()))
	}
	optsLocal = append(optsLocal, withContext(ctx))
	s := StartSpan(operationName, optsLocal...)
	if s != nil && s.pprofCtxActive != nil {
		ctx = s.pprofCtxActive
	}
	return s, ContextWithSpan(ctx, s)
}

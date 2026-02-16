// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package internal

import (
	"context"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion"
)

type executionTracedKey struct{}

// WithExecutionTraced marks ctx as being associated with an execution trace
// task. It is assumed that ctx already contains a trace task. The caller is
// responsible for ending the task.
//
// This is intended for a specific case where the database/sql contrib package
// only creates spans *after* an operation, in case the operation was
// unavailable, and thus execution trace tasks tied to the span only capture the
// very end. This function enables creating a task *before* creating a span, and
// communicating to the APM tracer that it does not need to create a task. In
// general, APM instrumentation should prefer creating tasks around the
// operation rather than after the fact, if possible.
func WithExecutionTraced(ctx context.Context) context.Context {
	return orchestrion.CtxWithValue(ctx, executionTracedKey{}, true)
}

// WithExecutionNotTraced marks that the context is *not* covered by an
// execution trace task. This is intended to prevent child spans (which inherit
// information from ctx) from being considered covered by a task, when an
// integration may create its own child span with its own execution trace task.
//
// When orchestrion is enabled, this pushes a value onto the per-goroutine GLS
// stack (unless the fast path is taken because no prior traced value exists).
// The caller must call PopExecutionTraced when the scope ends to avoid leaking
// GLS entries on long-lived goroutines.
func WithExecutionNotTraced(ctx context.Context) context.Context {
	if orchestrion.WrapContext(ctx).Value(executionTracedKey{}) == nil {
		// Fast path: if it wasn't marked before, we don't need to wrap
		// the context
		return ctx
	}
	return orchestrion.CtxWithValue(ctx, executionTracedKey{}, false)
}

// PopExecutionTraced pops the top executionTracedKey value from the GLS
// context stack. Must be called to pair with WithExecutionTraced or
// WithExecutionNotTraced when the associated scope ends.
func PopExecutionTraced() {
	orchestrion.GLSPopValue(executionTracedKey{})
}

// ScopedExecutionNotTraced marks ctx as not covered by an execution trace task
// and returns a cleanup function that pops the GLS entry. Unlike using
// WithExecutionNotTraced + PopExecutionTraced separately, the returned cleanup
// is goroutine-safe: it captures the pushing goroutine's GLS contextStack
// pointer and only pops if called from the same goroutine. This makes it safe
// for use with spans that may be finished on a different goroutine than the one
// that created them.
func ScopedExecutionNotTraced(ctx context.Context) (context.Context, func()) {
	newCtx := WithExecutionNotTraced(ctx)
	if newCtx == ctx {
		// Fast path: nothing was pushed, no cleanup needed.
		return ctx, glsNoop
	}
	return newCtx, orchestrion.GLSPopFunc(executionTracedKey{})
}

var glsNoop = func() {}

// IsExecutionTraced returns whether ctx is associated with an execution trace
// task, as indicated via WithExecutionTraced.
func IsExecutionTraced(ctx context.Context) bool {
	v := orchestrion.WrapContext(ctx).Value(executionTracedKey{})
	b, ok := v.(bool)
	return ok && b
}

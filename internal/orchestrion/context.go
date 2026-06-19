// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package orchestrion

import (
	"context"
	"sync/atomic"
)

// WrapContext returns the GLS-wrapped context if orchestrion is enabled, otherwise it returns the given parameter.
func WrapContext(ctx context.Context) context.Context {
	if !Enabled() {
		return ctx
	}

	if ctx != nil {
		if _, ok := ctx.(*glsContext); ok { // avoid (some) double wrapping
			return ctx
		}
	}

	if ctx == nil {
		ctx = context.Background()
	}

	return &glsContext{ctx}
}

// CtxWithValue runs context.WithValue, adds the result to the GLS slot of orchestrion, and returns it.
// If orchestrion is not enabled, it will run context.WithValue and return the result.
// Since we don't support cross-goroutine switch of the GLS we still run context.WithValue in the case
// we are switching goroutines.
func CtxWithValue(parent context.Context, key, val any) context.Context {
	if !Enabled() {
		return context.WithValue(parent, key, val)
	}

	getDDContextStack().Push(key, val)
	return context.WithValue(WrapContext(parent), key, val)
}

// GLSPopValue pops the value from the GLS slot of orchestrion and returns it. Using context.Context values usually does
// not require to pop any stack because the copy of each previous context makes the local variable in the scope disappear
// when the current function ends. But the GLS is a semi-global variable that can be accessed from any function in the
// stack, so we need to pop the value when we are done with it.
func GLSPopValue(key any) any {
	if !Enabled() {
		return nil
	}

	return getDDContextStack().Pop(key)
}

// GLSPopper releases a span's GLS entry. It is the goroutine-scoped popper
// captured at activation (via GLSPopFunc) and stored, atomically, in a
// [GLSPopperCell].
type GLSPopper func()

// GLSPopperCell holds a [GLSPopper] atomically. It is the type orchestrion
// injects as the popper field on Span and dyngo's operation (via
// add-struct-field, which requires a named type). Storing the popper in an
// atomic pointer makes the woven paths race-free: GLSDeactivate (woven into
// Span.Finish) and GLSReset (woven into Span.clear) can run concurrently on the
// same field when a span is finished on one goroutine while the tracer's span
// pool recycles it on another, and a repeated finish must run the popper at
// most once. The zero value is ready to use; a nil inner pointer means no
// popper is currently captured.
type GLSPopperCell struct {
	ptr atomic.Pointer[GLSPopper]
}

// GLSActivate is woven into span/operation activation (the tracer's
// ContextWithSpan and dyngo's RegisterOperation). It pushes val onto the current
// goroutine's GLS stack under key and records a goroutine-scoped popper into
// pop, capturing it only on the first activation so re-activating the same
// span/operation does not overwrite the popper its matching GLSDeactivate will
// run. The captured popper pops the top of the pushing goroutine's stack and is
// a no-op on any other goroutine, so a cross-goroutine finish can never corrupt
// an unrelated goroutine's stack.
//
// When ctxp is non-nil the parent context is wrapped (via WrapContext) so the
// returned context is also GLS-aware, matching the former in-source CtxWithValue.
// Everything is a no-op when orchestrion is disabled.
//
// Grouping the wrap, push and popper-capture here keeps the injected templates a
// single call and the logic unit-testable in plain go test. The companions are
// GLSDeactivate (finish) and GLSReset (span-pool reuse).
func GLSActivate(ctxp *context.Context, key, val any, pop *GLSPopperCell) {
	if !Enabled() {
		return
	}
	if ctxp != nil {
		*ctxp = WrapContext(*ctxp)
	}
	getDDContextStack().Push(key, val)
	if pop != nil {
		// Capture the popper only on the first activation (first-wins) so
		// re-activating the same span/operation does not overwrite the popper
		// its matching GLSDeactivate will run. CompareAndSwap keeps this
		// race-free if two activations ever overlap.
		fn := GLSPopper(GLSPopFunc(key))
		pop.ptr.CompareAndSwap(nil, &fn)
	}
}

// GLSDeactivate releases a span's GLS entry on finish. It marks the span
// reclaimable (so a cross-goroutine finish, whose popper is a no-op here, is
// still cleaned up by contextStack.Push on its next push) and invokes the
// captured popper exactly once, clearing it so a repeated finish does not pop
// again. reclaimable and pop are the fields orchestrion injects onto the span;
// passing them by pointer lets injected span-finish advice deactivate in one
// call.
func GLSDeactivate(reclaimable *atomic.Bool, pop *GLSPopperCell) {
	if reclaimable != nil {
		reclaimable.Store(true)
	}
	if pop == nil {
		return
	}
	// Atomically read and clear the popper so a repeated or concurrent finish
	// invokes it at most once.
	if fn := pop.ptr.Swap(nil); fn != nil {
		(*fn)()
	}
}

// GLSReset clears the GLS bookkeeping fields orchestrion injects onto a span so
// that a span returned to the tracer's pool and later reused is never treated as
// reclaimable or left carrying a stale popper. It is woven into Span.clear. The
// reclaimable argument may be nil (dyngo operations carry no reclaim flag).
func GLSReset(reclaimable *atomic.Bool, pop *GLSPopperCell) {
	if reclaimable != nil {
		reclaimable.Store(false)
	}
	if pop != nil {
		pop.ptr.Store(nil)
	}
}

// GLSPopFunc returns a function that pops key from the GLS context stack of the
// goroutine that called GLSPopFunc. The returned function is safe to call from
// any goroutine: it compares the current goroutine's GLS contextStack pointer
// with the one captured at creation time and only pops if they match (i.e.,
// same goroutine). On a different goroutine the pop is a no-op, preventing
// accidental corruption of another goroutine's GLS state.
func GLSPopFunc(key any) func() {
	if !Enabled() {
		return glsNoop
	}
	pushStack := getDDContextStack()
	return func() {
		if gls := getDDGLS(); gls != nil && gls.(*contextStack) == pushStack {
			pushStack.Pop(key)
		}
	}
}

var glsNoop = func() {}

// GLSStackDepth returns the total number of entries in the current goroutine's
// GLS context stack. Returns 0 if orchestrion is not enabled. This is intended
// for use in tests to detect GLS leaks.
func GLSStackDepth() int {
	if !Enabled() {
		return 0
	}
	return getDDContextStack().Depth()
}

var _ context.Context = (*glsContext)(nil)

type glsContext struct {
	context.Context
}

func (g *glsContext) Value(key any) any {
	if !Enabled() {
		return g.Context.Value(key)
	}

	// Check the explicit context chain first: an explicitly propagated value
	// takes priority over goroutine-local storage (GLS). GLS serves as a
	// fallback for when no value is present in the context chain, enabling
	// implicit span propagation through un-instrumented call sites.
	if val := g.Context.Value(key); val != nil {
		return val
	}

	if val := getDDContextStack().Peek(key); val != nil {
		return val
	}

	return nil
}

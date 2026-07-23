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

	getDDContextStack().Push(key, val, nil) // nil = non-reclaimable (no lifecycle cell)
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

// GLSDoneCell holds the heap-allocated liveness cell for a span's current GLS
// lifecycle. It is the type orchestrion injects as the __dd_glsDone field on
// Span (via add-struct-field, which requires a named type).
//
// One *atomic.Bool cell is allocated on a span's first activation and shared by
// every subsequent activation of that span (GLSActivate reuses it), so all of
// the span's contextStack entries observe a single liveness signal. Each entry
// keeps its own pointer to the cell. When the span finishes, GLSDeactivate sets
// the cell to true, marking every entry drain-eligible. When the span is
// recycled by the pool, GLSReset clears this ptr — but the contextStack entries
// retain their own references to the now-true cell, so it outlives the span's
// current lifecycle and the next Push drains them. The reused span starts with
// ptr == nil and allocates a fresh cell on its next activation: no ABA.
//
// The zero value is ready to use.
type GLSDoneCell struct {
	ptr atomic.Pointer[atomic.Bool]
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
// done, when non-nil, holds the span's liveness cell (a *atomic.Bool). The
// first activation allocates it; later activations of the same span reuse it,
// so every stack entry for the span shares one liveness signal and they are all
// marked done together at Finish. The cell pointer is passed to
// contextStack.Push, tying the entry to the cell — not to the span itself —
// which is what makes reclaim safe across span-pool reuse. A live span
// re-activated on another goroutine is never marked done here (that would drain
// a still-live entry); see the reuse comment in the body.
//
// When done is nil (e.g. dyngo operations that never cross goroutine boundaries)
// the stack entry carries no done cell and is never drained by Push. When
// ctxp is non-nil the parent context is wrapped (via WrapContext) so the
// returned context is also GLS-aware. Everything is a no-op when orchestrion
// is disabled.
//
// Grouping the wrap, push, popper-capture, and cell allocation here keeps the
// injected templates a single call and the logic unit-testable in plain go test.
// The companions are GLSDeactivate (finish) and GLSReset (span-pool reuse).
func GLSActivate(ctxp *context.Context, key, val any, pop *GLSPopperCell, done *GLSDoneCell) {
	if !Enabled() {
		return
	}
	if ctxp != nil {
		*ctxp = WrapContext(*ctxp)
	}
	var cell *atomic.Bool
	if done != nil {
		if existing := done.ptr.Load(); existing != nil {
			// Reuse this span's existing liveness cell — one cell per span
			// lifecycle, shared by every activation. All of the span's stack
			// entries therefore observe a single liveness signal and become
			// drain-eligible together when Finish marks the cell.
			//
			// We must NOT allocate a fresh cell and mark the old one done here:
			// when a still-live span is propagated to another goroutine (the
			// owner ran ContextWithSpan, then a worker re-activates it before
			// Finish), marking the previous cell done would make a STILL-LIVE
			// entry drain-eligible. The owner's next Push would drop it, so after
			// a child span pops, the GLS fallback would no longer restore the
			// (unfinished) parent — corrupting cross-goroutine live propagation.
			// See orchestrion#782 review.
			//
			// A finished span recycled by the span pool has its cell cleared by
			// GLSReset (ptr == nil), so it allocates a fresh cell below on its
			// next activation: no ABA. A cell that is already true here (Finish
			// ran before this activation, the cross-goroutine korECM pattern) is
			// likewise reused, making the entry immediately drain-eligible.
			cell = existing
		} else {
			// First activation of this lifecycle: allocate the cell. CompareAndSwap
			// so concurrent first-activations of the same span converge on one
			// cell rather than each pushing an entry with its own (one of which
			// Finish would then never mark, leaking it).
			cell = new(atomic.Bool)
			if !done.ptr.CompareAndSwap(nil, cell) {
				cell = done.ptr.Load()
			}
		}
	}
	getDDContextStack().Push(key, val, cell)
	if pop != nil && pop.ptr.Load() == nil {
		// Capture the popper only on the first activation (first-wins) so
		// re-activating the same span/operation does not overwrite the popper
		// its matching GLSDeactivate will run. The pre-check skips the
		// GLSPopFunc closure allocation when the field is already set (common
		// on re-activation). CompareAndSwap keeps this race-free when two
		// goroutines activate concurrently: only one CAS wins; the other's
		// closure is discarded, preserving first-wins semantics.
		fn := GLSPopper(GLSPopFunc(key))
		pop.ptr.CompareAndSwap(nil, &fn)
	}
}

// GLSDeactivate releases a span's GLS entry on finish. It ensures the liveness
// cell exists and is marked done, then invokes the captured popper exactly once.
//
// Two patterns are supported:
//   - Normal path (ContextWithSpan before Finish): GLSActivate already set the
//     done cell via GLSActivate → GLSDeactivate simply loads and marks it true.
//   - Cross-goroutine path (Finish before ContextWithSpan): the done cell does not
//     exist yet. GLSDeactivate creates it pre-marked true via CAS so that when
//     GLSActivate runs later it finds a true cell, reuses it, and the resulting
//     stack entry is immediately drain-eligible — preventing the GLS stack from
//     growing unbounded (orchestrion#782 / korECM pattern).
//
// done and pop are the fields orchestrion injects onto the span; passing them by
// pointer lets injected span-finish advice deactivate in one call. done is nil
// for dyngo operations (they rely solely on the goroutine-scoped popper and
// never cross goroutine boundaries).
func GLSDeactivate(done *GLSDoneCell, pop *GLSPopperCell) {
	if done != nil {
		if cell := done.ptr.Load(); cell != nil {
			// Normal path: cell already exists (GLSActivate ran first).
			cell.Store(true)
		} else {
			// Cross-goroutine path: Finish called before ContextWithSpan. Create
			// a pre-marked cell so GLSActivate can reuse it later, making the
			// resulting stack entry immediately drain-eligible.
			preMarked := new(atomic.Bool)
			preMarked.Store(true)
			if !done.ptr.CompareAndSwap(nil, preMarked) {
				// A concurrent GLSActivate set the cell first; mark it.
				done.ptr.Load().Store(true)
			}
		}
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
// that a span returned to the tracer's pool and later reused starts with a clean
// slate: no stale popper and no stale done cell. It is woven into Span.clear.
// Clearing the done cell drops the span's reference to it — the contextStack
// entry retains its own copy of the pointer and can still observe the cell's
// true value (set by the preceding GLSDeactivate). A reused span receives a
// fresh cell on its next GLSActivate call, preventing the ABA hazard.
// done is nil for dyngo operations (they carry no liveness cell).
func GLSReset(done *GLSDoneCell, pop *GLSPopperCell) {
	if done != nil {
		done.ptr.Store(nil)
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

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package orchestrion

import (
	"context"
	"sync/atomic"
)

// WrapContext returns the GLS-wrapped context if the GLS is woven in, otherwise it returns the given parameter.
func WrapContext(ctx context.Context) context.Context {
	if !glsActive() {
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
// If the GLS is not woven in, it will run context.WithValue and return the result.
// Since we don't support cross-goroutine switch of the GLS we still run context.WithValue in the case
// we are switching goroutines.
func CtxWithValue(parent context.Context, key, val any) context.Context {
	if !glsActive() {
		return context.WithValue(parent, key, val)
	}

	getDDContextStack().Push(key, val, nil, nil) // nil cell: this value never reports itself finished
	return context.WithValue(WrapContext(parent), key, val)
}

// CtxWithScopedValue is [CtxWithValue] with a matching scope exit: it pushes val
// under key and returns the derived context along with a cleanup that removes
// the entry this call pushed, plus anything still stacked above it.
//
// Use it wherever the scope can close out of order. [GLSPopValue] takes the top
// of the stack, so a non-LIFO close pops an unrelated entry and strands its own,
// leaving a scope on the GLS after it ended.
//
// The cleanup is goroutine-scoped (see [GLSPopFunc]): off the pushing goroutine
// it does nothing, so it cannot corrupt a foreign stack. Calling it more than
// once is harmless — after the first call the token is gone and the rest are
// no-ops.
//
// When the GLS is not woven in, this degrades to context.WithValue and a cleanup
// that does nothing.
func CtxWithScopedValue(parent context.Context, key, val any) (context.Context, func()) {
	if !glsActive() {
		return context.WithValue(parent, key, val), glsNoop
	}

	token := getDDContextStack().Push(key, val, nil, nil) // nil cell: the returned cleanup is the only exit
	return context.WithValue(WrapContext(parent), key, val), GLSPopEntryFunc(key, token)
}

// GLSPopValue pops the value from the GLS slot of orchestrion and returns it. Using context.Context values usually does
// not require to pop any stack because the copy of each previous context makes the local variable in the scope disappear
// when the current function ends. But the GLS is a semi-global variable that can be accessed from any function in the
// stack, so we need to pop the value when we are done with it.
//
// This takes the top of the stack, so it is only correct for keys whose scopes
// close strictly LIFO. Anything else must use [CtxWithScopedValue].
func GLSPopValue(key any) any {
	if !glsActive() {
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
	ptr atomic.Pointer[glsExit]
}

// glsExit is what a [GLSPopperCell] actually holds: the captured exit together
// with the token of the entry it closes.
//
// The token is here so that removing an entry can tell whether the exit it is
// about to discard is that entry's, or a different and still-live activation's.
// One value activated more than once shares a single cell, and first-wins means
// the exit names the FIRST activation. Activate B, then A, then B again: closing
// A sweeps A and the upper B, but the cell those entries point at is the lower
// B's only way out, so clearing it unconditionally strands the lower B. Pairing
// the two in one struct keeps them consistent under a single atomic load, which
// two separate fields could not do.
type glsExit struct {
	pop   GLSPopper
	token uint64
}

// GLSDoneCell holds the liveness cell for a span's current GLS lifecycle. It is
// the type orchestrion injects as the __dd_glsDone field on Span (via
// add-struct-field, which requires a named type).
//
// One *atomic.Bool cell is allocated on a span's first activation and shared by
// every later activation of that span, so all of the span's stack entries observe
// a single liveness signal and are marked done together at Finish. Each entry
// keeps its own pointer to the cell.
//
// The indirection is what decouples the signal from the span. When the span pool
// recycles the span, GLSReset clears this field — but the stack entries still
// hold the cell, so the true set by GLSDeactivate outlives the span's lifecycle
// and the next Push drains them. The reused span starts with a nil pointer and
// allocates a fresh cell on its next activation, so a recycled span can never
// report a scope it did not open. Storing the bit on the span instead lets clear
// flip it back to false, which is the ABA this design removes.
//
// The zero value is ready to use.
type GLSDoneCell struct {
	ptr atomic.Pointer[atomic.Bool]
}

// doneSentinel is the cell installed when a span is finished before it was ever
// activated, which under orchestrion is every span that finishes without passing
// through ContextWithSpan. That case needs no per-span state, only the immutable
// "already done", so one shared cell serves all of them and saves an allocation
// per StartSpan/Finish pair on exactly the high-throughput paths the span pool
// exists for.
//
// Sharing is safe because nothing ever writes false into a cell: GLSActivate only
// ever allocates new ones, GLSDeactivate only stores true, and GLSReset clears the
// pointer rather than the value. A subsequent GLSActivate that finds this cell
// reuses it and pushes an entry that is drain-eligible on arrival, which is the
// intended behaviour for a span whose finish already happened.
var doneSentinel = func() *atomic.Bool {
	b := new(atomic.Bool)
	b.Store(true)
	return b
}()

// GLSActivate is woven into span/operation activation (the tracer's
// ContextWithSpan and dyngo's RegisterOperation). It pushes val onto the current
// goroutine's GLS stack under key and records a goroutine-scoped popper into
// pop, capturing it only on the first activation so re-activating the same
// span/operation does not overwrite the popper its matching GLSDeactivate will
// run. The captured popper closes the scope this push opened — that entry and
// anything still stacked above it — and is a no-op on any other goroutine, so a
// cross-goroutine finish can never corrupt an unrelated goroutine's stack.
//
// First-wins and scope exit compose: a re-activated span keeps the popper from
// its first push, so deactivating removes the scope from where the span first
// became active, taking any later duplicate push of the same span with it.
//
// done, when non-nil, holds the span's liveness cell, which is passed to
// contextStack.Push as the entry's cell. It follows the same first-wins rule as
// the popper: the first activation allocates the cell and every later activation
// of the same span reuses it, so all of the span's entries share one signal and
// Finish marks them together. When done is nil (dyngo operations, which never
// cross a goroutine boundary) the entry carries no cell and is never drained.
//
// When ctxp is non-nil the parent context is wrapped (via WrapContext) so the
// returned context is also GLS-aware, matching the former in-source CtxWithValue.
// Everything is a no-op when the GLS is not woven in.
//
// Grouping the wrap, push, popper-capture and cell allocation here keeps the
// injected templates a single call and the logic unit-testable in plain go test.
// The companions are GLSDeactivate (finish) and GLSReset (span-pool reuse).
func GLSActivate(ctxp *context.Context, key, val any, pop *GLSPopperCell, done *GLSDoneCell) {
	if !glsActive() {
		return
	}
	if ctxp != nil {
		*ctxp = WrapContext(*ctxp)
	}
	var cell *atomic.Bool
	if done != nil {
		if existing := done.ptr.Load(); existing != nil {
			// Reuse the span's cell: one cell per lifecycle, shared by every
			// activation.
			//
			// Allocating a fresh cell here and marking the old one done would be
			// wrong. A still-live span propagated to a second goroutine activates
			// again before Finish, and marking the previous cell would make a live
			// entry drain-eligible: the first goroutine's next Push drops it, so
			// after a child scope closes the GLS no longer restores the unfinished
			// parent and the child's successor reparents. That is what silently
			// moved kafka.consume off its produce parent.
			//
			// A cell that already reads true is likewise reused rather than
			// replaced: Finish ran before this activation (the cross-goroutine
			// order), so the entry should be drain-eligible the moment it lands.
			// A fresh false cell would never be marked and would leak one entry
			// per record.
			cell = existing
		} else {
			// First activation of this lifecycle — including the first after the
			// pool recycled the span, since GLSReset left this nil. CompareAndSwap
			// so two concurrent first activations converge on one cell; otherwise
			// each pushes an entry with its own, and Finish marks only one of them.
			fresh := new(atomic.Bool)
			if done.ptr.CompareAndSwap(nil, fresh) {
				cell = fresh
			} else if existing := done.ptr.Load(); existing != nil {
				// Lost the race; adopt the cell that won, exactly as the reuse
				// branch above would have.
				cell = existing
			} else {
				// Non-nil at the CompareAndSwap and nil at the load, so a GLSReset
				// ran between them. GLSReset is woven into Span.clear, which the
				// pool reaches only after Finish, so the lifecycle this activation
				// was for has both ended and been recycled: the scope this entry
				// would represent is over before the entry exists.
				//
				// So it must be dead on arrival, and the shared already-done cell
				// says exactly that. The two alternatives are both wrong in the same
				// direction. Installing fresh would succeed now that the field is
				// nil again, but Finish has already returned and nothing is left to
				// mark it, leaving a permanently live entry — and taking nil is no
				// better, since a nil cell is never drained and never skipped
				// either. Both surface a recycled span as the parent of unrelated
				// work, which is the merge this whole line of work exists to stop.
				//
				// The sentinel is deliberately not installed. done.ptr belongs to
				// whatever lifecycle the recycled span is serving now, and writing
				// a done cell into it would mark that live scope finished.
				cell = doneSentinel
			}
		}
	}
	token := getDDContextStack().Push(key, val, cell, pop)
	if pop != nil && pop.ptr.Load() == nil {
		// Capture the popper only on the first activation (first-wins) so
		// re-activating the same span/operation does not overwrite the popper
		// its matching GLSDeactivate will run. The pre-check skips the
		// GLSPopFunc closure allocation when the field is already set (common
		// on re-activation). CompareAndSwap keeps this race-free when two
		// goroutines activate concurrently: only one CAS wins; the other's
		// closure is discarded, preserving first-wins semantics.
		pop.ptr.CompareAndSwap(nil, &glsExit{pop: GLSPopFunc(key, token), token: token})
	}
}

// GLSDeactivate releases a span's GLS entry on finish. It marks the liveness
// cell done and invokes the captured popper exactly once, clearing it so a
// repeated finish does not pop again.
//
// The cell covers the case the popper cannot: after a cross-goroutine finish the
// popper is a no-op here, so the entry stays on the pushing goroutine's stack,
// where contextStack.Peek refuses to hand it out as the active span and the next
// contextStack.Push drops it.
//
// Both activation orders are handled. Normally GLSActivate ran first and this
// just marks the cell it allocated. When Finish runs before the span is ever put
// into a context — the cross-goroutine order — there is no cell yet, so one is
// created already marked, and the GLSActivate that follows reuses it and pushes
// an entry that is drain-eligible on arrival. Without that, the stack would grow
// by one entry per record.
//
// done and pop are the fields orchestrion injects onto the span; passing them by
// pointer lets injected span-finish advice deactivate in one call. done is nil for
// dyngo operations, which rely on the goroutine-scoped popper alone.
func GLSDeactivate(done *GLSDoneCell, pop *GLSPopperCell) {
	if done != nil {
		if cell := done.ptr.Load(); cell != nil {
			cell.Store(true)
		} else {
			if !done.ptr.CompareAndSwap(nil, doneSentinel) {
				// A concurrent GLSActivate installed its cell first; mark that one
				// instead. The failed CAS only proves the field was non-nil at that
				// instant, so this load is checked: a GLSReset racing in between
				// leaves nothing to mark, and dereferencing it would panic.
				if cell := done.ptr.Load(); cell != nil {
					cell.Store(true)
				}
			}
		}
	}
	if pop == nil {
		return
	}
	// Atomically read and clear the popper so a repeated or concurrent finish
	// invokes it at most once.
	if e := pop.ptr.Swap(nil); e != nil {
		e.pop()
	}
}

// GLSReset clears the GLS bookkeeping fields orchestrion injects onto a span so
// that a span returned to the tracer's pool and later reused starts clean: no
// stale popper, and no cell describing a scope that belonged to the previous
// lifecycle. It is woven into Span.clear.
//
// Clearing done only drops the span's pointer to the cell. Stack entries keep
// their own, so the true GLSDeactivate stored is still visible to the drain long
// after the span object has been handed to an unrelated scope, and the reused
// span gets a fresh cell on its next activation. Resetting a bit on the span
// instead would flip those entries back to live — the ABA. done is nil for dyngo
// operations, which carry no cell.
func GLSReset(done *GLSDoneCell, pop *GLSPopperCell) {
	if done != nil {
		done.ptr.Store(nil)
	}
	if pop != nil {
		pop.ptr.Store(nil)
	}
}

// GLSPopFunc returns a function that closes the scope token opened under key on
// the GLS context stack of the goroutine that called GLSPopFunc. token comes
// from the [contextStack.Push] that opened the scope.
//
// The returned function is safe to call from any goroutine: it compares the
// current goroutine's GLS contextStack pointer with the one captured at creation
// time and only acts if they match (i.e., same goroutine). On a different
// goroutine it is a no-op, preventing accidental corruption of another
// goroutine's GLS state — which is also why the liveness cell on each [entry]
// still has to exist: a cross-goroutine finish cannot reach the foreign slice at
// all, so those entries are only ever cleaned up lazily.
//
// The exit is by token, not by position, so an out-of-order close removes the
// entry it actually opened (plus anything still stacked above it, which was
// opened inside that scope) rather than whatever is on top. A token that has
// already been removed matches nothing, so a late or repeated call does nothing.
func GLSPopFunc(key any, token uint64) func() {
	if !glsActive() {
		return glsNoop
	}
	pushStack := getDDContextStack()
	return func() {
		if gls := getDDGLS(); gls != nil && gls.(*contextStack) == pushStack {
			pushStack.PopScope(key, token)
		}
	}
}

// GLSPopEntryFunc is [GLSPopFunc] for a key whose entries are independent scopes
// rather than nested ones: it removes only the entry it opened, leaving anything
// pushed above it to be closed by whatever owns it. Use this whenever the key is
// shared with a positional [GLSPopValue] exit, which would otherwise reach past
// its own scope once its entry had been swept. See [contextStack.PopEntry].
func GLSPopEntryFunc(key any, token uint64) func() {
	if !glsActive() {
		return glsNoop
	}
	pushStack := getDDContextStack()
	return func() {
		if gls := getDDGLS(); gls != nil && gls.(*contextStack) == pushStack {
			pushStack.PopEntry(key, token)
		}
	}
}

var glsNoop = func() {}

// GLSStackDepth returns the total number of entries in the current goroutine's
// GLS context stack. Returns 0 if the GLS is not woven in. This is intended
// for use in tests to detect GLS leaks.
func GLSStackDepth() int {
	if !glsActive() {
		return 0
	}
	return getDDContextStack().Depth()
}

var _ context.Context = (*glsContext)(nil)

type glsContext struct {
	context.Context
}

func (g *glsContext) Value(key any) any {
	if !glsActive() {
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

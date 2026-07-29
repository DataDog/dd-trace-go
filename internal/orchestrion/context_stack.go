// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package orchestrion

import "slices"

// entry is one pushed value together with the token identifying its scope.
//
// A token is what makes scope exit possible. A position cannot identify a scope:
// push, pop, push again lands the new value at the same index, so an index
// captured at push time can name an entirely different scope by the time the
// exit runs. Tokens are handed out by a counter that only ever increases, so a
// stale one matches nothing.
type entry struct {
	val   any
	token uint64
	// pop is the cell holding the exit captured for this value, so that removing
	// the entry can invalidate it. Without that, a value swept away by an
	// enclosing scope keeps an exit naming a token that no longer exists: its
	// next activation sees a non-nil cell, keeps the stale exit under first-wins,
	// and the entry that activation pushes is never closed by anything.
	pop *GLSPopperCell
}

// contextStack is stored in the GLS slot of runtime.g inserted by orchestrion.
// It holds context values shared within a single goroutine.
//
// The stack is per-goroutine and unsynchronised by design, so next is a plain
// counter rather than an atomic. It lives on the struct rather than alongside
// each key's slice because Pop and PopScope delete a key once it empties: a
// per-key counter would restart from zero on the next push under that key and
// re-issue a token some popper is still holding.
//
// TODO: handle cross-goroutine context values
type contextStack struct {
	stacks map[any][]entry
	// next is the last token handed out. Push pre-increments, so the first
	// token is 1 and 0 is free to mean "no scope" — what Push returns when it
	// pushed nothing, and a value PopScope never matches.
	next uint64
}

// reclaimable is implemented by GLS values that can signal they no longer
// represent a live scope. It is used to bound GLS growth when a value is pushed
// on one goroutine but its lifecycle ends on another, so the matching pop never
// runs on the pushing goroutine (e.g. a *tracer.Span pushed via ContextWithSpan
// and finished elsewhere). The orchestrion package intentionally does not import
// the tracer; values opt in by implementing this single method.
//
// Reporting true has two consequences: Push may drop the entry, and Peek will
// never return it as the active scope. Implementations must therefore only
// report true once the value can no longer legitimately parent new work, and
// must not flip back to false while the entry is still reachable from a stack.
//
// A pointer implementation must report true for a nil receiver. Reporting false
// would mark a nil value live, which shadows every entry beneath it in Peek and
// stops Push's drain dead.
type reclaimable interface {
	// GLSReclaimable reports whether this value has stopped representing a live
	// scope. Implementations must be safe to call from any goroutine.
	GLSReclaimable() bool
}

// isReclaimable reports whether v has opted into [reclaimable] and currently
// reports itself dead. Values that do not implement the interface carry no
// finished state (e.g. the bool stored under executionTracedKey) and so are
// always treated as live.
func isReclaimable(v any) bool {
	r, ok := v.(reclaimable)
	return ok && r.GLSReclaimable()
}

// getDDContextStack is a main way to access the GLS slot of runtime.g inserted by orchestrion. This function should not be
// called if the enabled variable is false.
func getDDContextStack() *contextStack {
	if gls := getDDGLS(); gls != nil {
		return gls.(*contextStack)
	}

	newStack := &contextStack{}
	setDDGLS(newStack)
	return newStack
}

// PopEntry removes exactly the entry token opened under key, leaving anything
// pushed above it in place, and reports whether it found one.
//
// This is the counterpart to [contextStack.PopScope] for a key whose entries are
// not nested scopes. PopScope also drops everything above its target, which is
// right when those entries were opened inside the scope now ending and so cannot
// outlive it. It is wrong when they are independent scopes owning their own
// exits: sweeping one away does not cancel its exit, and if that exit is
// positional ([contextStack.Pop]) it then removes an unrelated entry further
// down the stack.
//
// internal.executionTracedKey is exactly that case. It holds plain bools pushed
// by WithExecutionTraced, whose paired PopExecutionTraced takes the top of the
// stack, sharing the key with the scope-exact override from
// ScopedExecutionNotTraced. Removing only our own entry keeps the two exits from
// colliding while still fixing what PopScope was introduced for here: the
// override is removed by identity, so a non-LIFO exit can no longer strand it
// and leave the stack claiming "not traced" for everything after it.
func (s *contextStack) PopEntry(key any, token uint64) bool {
	if s == nil || s.stacks == nil || token == 0 {
		return false
	}

	stack := s.stacks[key]
	for i, e := range slices.Backward(stack) {
		if e.token == token {
			s.remove(key, stack, i)
			return true
		}
		if e.token < token {
			return false
		}
	}

	return false
}

// remove deletes the single entry at i. Deleting from the middle keeps tokens
// ascending, so [contextStack.PopScope]'s and [contextStack.PopEntry]'s early
// exit stays valid; slices.Delete zeroes the vacated tail so a dropped value is
// not retained.
func (s *contextStack) remove(key any, stack []entry, i int) {
	invalidatePoppers(stack[i : i+1])
	stack = slices.Delete(stack, i, i+1)

	if len(stack) == 0 {
		delete(s.stacks, key)
		return
	}
	s.stacks[key] = stack
}

// invalidatePoppers clears the captured exit of every entry being removed, so a
// value activated again later captures a fresh one for its new token instead of
// keeping an exit that names a scope already gone.
//
// Clearing is safe for an entry whose owner has not finished yet: its entry is
// being removed here, so there is nothing left for its exit to do, and
// GLSDeactivate's Swap simply finds nil and runs nothing.
func invalidatePoppers(removed []entry) {
	for _, e := range removed {
		if e.pop == nil {
			continue
		}
		// Only discard the exit if it is this entry's. A value activated more than
		// once shares one cell whose exit names the first activation, so an entry
		// removed from above must leave it alone — it is the surviving lower
		// entry's only way out. CompareAndSwap rather than Store so an exit
		// captured between the load and here is not clobbered.
		if cur := e.pop.ptr.Load(); cur != nil && cur.token == e.token {
			e.pop.ptr.CompareAndSwap(cur, nil)
		}
	}
}

// truncate drops stack[i:] from key's slice. The removed elements are zeroed so
// the GC can collect the values they held, and the key is deleted outright once
// nothing remains under it, keeping the map from retaining empty slices.
//
// stack is passed in rather than re-read because every caller already holds it.
func (s *contextStack) truncate(key any, stack []entry, i int) {
	invalidatePoppers(stack[i:])
	clear(stack[i:])
	stack = stack[:i]

	if len(stack) == 0 {
		delete(s.stacks, key)
		return
	}
	s.stacks[key] = stack
}

// Peek returns the innermost live context from the stack without removing it.
//
// Entries reporting themselves reclaimable (see [reclaimable]) are skipped: they
// are finished scopes whose matching pop ran on another goroutine. The GLS is
// only consulted when the explicit context chain carries nothing (see
// [glsContext.Value]), so it must surface a live scope or none at all —
// returning a finished span would let StartSpanFromContext adopt it as a parent
// and collapse unrelated requests into one trace.
//
// Peek therefore no longer returns the same element as Pop: Pop takes the top
// unconditionally, Peek takes the innermost live entry. Callers that need the
// literal top must not use Peek.
//
// The walk stops at the first live entry, so its cost is the length of the
// trailing run of reclaimable entries. Push drains that run, so it does not
// accumulate across pushes, but nothing drains it between them — a read-heavy
// stretch pays the whole run on every read. See BenchmarkPeekSkipReclaimed for
// the measured cost. Interleaved live and finished entries do not degrade the
// walk: it stops at the first live one from the top.
//
// Peek does not mutate the stack. Values that do not implement [reclaimable] are
// returned as-is.
//
// This addresses a finished entry being surfaced as a parent. It is not what
// keeps a live entry from outliving the scope beneath it — that is [PopScope]'s
// job, since no read-side guard can tell a live survivor from a legitimately
// nested scope.
func (s *contextStack) Peek(key any) any {
	if s == nil || s.stacks == nil {
		return nil
	}

	for _, e := range slices.Backward(s.stacks[key]) {
		if !isReclaimable(e.val) {
			return e.val
		}
	}

	return nil
}

// Push adds a context to the stack and returns the token identifying the scope
// it just opened. Pass that token to [PopScope] to close the scope; a zero
// return means nothing was pushed and there is nothing to close.
//
// Before appending, Push drops any trailing entries that report themselves
// reclaimable (see [reclaimable]). This bounds GLS growth when values are
// pushed on one goroutine but their lifecycle ends on another, so the pop
// never runs on this goroutine. Reads only ever surface a live entry (Peek
// skips reclaimable ones), and buried entries exist solely to be restored
// after a Pop; a reclaimable (e.g. finished) entry can never be a meaningful
// restore target, so dropping it preserves stack semantics. Entries whose type
// does not implement [reclaimable] (e.g. the bool stored under
// executionTracedKey) are never dropped.
func (s *contextStack) Push(key, val any, pop *GLSPopperCell) uint64 {
	if s == nil {
		return 0
	}
	if s.stacks == nil {
		s.stacks = make(map[any][]entry)
	}

	stack := s.stacks[key]
	for len(stack) > 0 && isReclaimable(stack[len(stack)-1].val) {
		stack[len(stack)-1] = entry{} // drop reference so GC can collect the value
		stack = stack[:len(stack)-1]
	}

	s.next++
	s.stacks[key] = append(stack, entry{val: val, token: s.next, pop: pop})
	return s.next
}

// PopScope closes the scope token opened under key: it removes that entry and
// every entry pushed above it, and reports whether it found one to remove.
//
// This is what [Pop] cannot do. Pop takes the top of the stack, so any finish
// that is not strictly LIFO takes somebody else's entry and strands its own —
// leaving a scope on the stack after it ended, where a later read adopts it as
// the active one. Removing everything above the target is the point rather than
// a side effect: those entries were opened inside the scope now ending, so none
// of them can outlive it either.
//
// Tokens ascend from the bottom of a key's slice, so the walk from the top can
// stop as soon as it passes below the target: a smaller token means the scope is
// already gone and there is nothing to do. That makes a repeated or late exit a
// cheap no-op rather than a destructive one — the guarantee a stale token needs,
// since the position it once occupied may now belong to an unrelated scope.
func (s *contextStack) PopScope(key any, token uint64) bool {
	if s == nil || s.stacks == nil || token == 0 {
		return false
	}

	stack := s.stacks[key]
	for i, e := range slices.Backward(stack) {
		if e.token == token {
			s.truncate(key, stack, i)
			return true
		}
		if e.token < token {
			return false
		}
	}

	return false
}

// Pop removes the top context from the stack and returns it.
//
// Prefer [PopScope] for anything that can finish out of order: Pop matches
// position, not identity, so it removes whatever happens to be on top.
func (s *contextStack) Pop(key any) any {
	if s == nil || s.stacks == nil {
		return nil
	}

	stack := s.stacks[key]
	if len(stack) == 0 {
		return nil
	}

	last := len(stack) - 1
	val := stack[last].val
	s.truncate(key, stack, last)

	return val
}

// Depth returns the total number of entries across all keys in the stack.
// This is useful for detecting GLS leaks where entries are pushed but never popped.
func (s *contextStack) Depth() int {
	if s == nil || s.stacks == nil {
		return 0
	}

	n := 0
	for _, stack := range s.stacks {
		n += len(stack)
	}
	return n
}

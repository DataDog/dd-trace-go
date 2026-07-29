// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package orchestrion

import "slices"

// contextStack is stored in the GLS slot of runtime.g inserted by orchestrion.
// It holds context values shared within a single goroutine.
// TODO: handle cross-goroutine context values
type contextStack map[any][]any

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
// This addresses a finished entry being surfaced as a parent. It does not help
// when a still-live entry outlives the scope beneath it: because the pop matches
// position rather than identity, a scope can be left buried under a live sibling
// that then parents unrelated later work. Fixing that needs scope-exit semantics
// (removing an entry and everything above it), not a read-side guard.
func (s *contextStack) Peek(key any) any {
	if s == nil || *s == nil {
		return nil
	}

	for _, v := range slices.Backward((*s)[key]) {
		if !isReclaimable(v) {
			return v
		}
	}

	return nil
}

// Push adds a context to the stack.
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
func (s *contextStack) Push(key, val any) {
	if s == nil || *s == nil {
		return
	}

	stack := (*s)[key]
	for len(stack) > 0 && isReclaimable(stack[len(stack)-1]) {
		stack[len(stack)-1] = nil // drop reference so GC can collect the value
		stack = stack[:len(stack)-1]
	}

	(*s)[key] = append(stack, val)
}

// Pop removes the top context from the stack and returns it.
func (s *contextStack) Pop(key any) any {
	if s == nil || *s == nil {
		return nil
	}

	stack, ok := (*s)[key]
	if !ok || len(stack) == 0 {
		return nil
	}

	lastIdx := len(stack) - 1
	val := stack[lastIdx]
	// slices.Delete zeroes removed elements in the backing array,
	// allowing GC to collect popped values.
	stack = slices.Delete(stack, lastIdx, lastIdx+1)

	if len(stack) == 0 {
		delete(*s, key)
	} else {
		(*s)[key] = stack
	}

	return val
}

// Depth returns the total number of entries across all keys in the stack.
// This is useful for detecting GLS leaks where entries are pushed but never popped.
func (s *contextStack) Depth() int {
	if s == nil || *s == nil {
		return 0
	}

	n := 0
	for _, stack := range *s {
		n += len(stack)
	}
	return n
}

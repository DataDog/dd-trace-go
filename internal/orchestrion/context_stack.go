// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package orchestrion

import (
	"slices"
	"sync/atomic"
)

// contextStack is stored in the GLS slot of runtime.g inserted by orchestrion.
// It holds context values shared within a single goroutine.
// TODO: handle cross-goroutine context values
type contextStack map[any][]stackEntry

// stackEntry is one element in a contextStack slice. done is non-nil for
// values that participate in the GLS reclaim lifecycle (spans, dyngo operations
// etc.). A nil done means the entry is never drained by Push (e.g. the bool
// stored under executionTracedKey).
type stackEntry struct {
	value any
	done  *atomic.Bool
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

// Peek returns the topmost live context from the stack without removing it.
//
// Entries whose done cell reads true are skipped: a finished value is no longer
// a live scope, and under the experimental span pool the recyclable object it
// points at may already have been reset and reused for an unrelated span.
// Returning it would surface a stale (or recycled, hence wrong) span as the
// active one from SpanFromContext's GLS fallback. Skipping such entries here
// closes that read window; contextStack.Push drains them on the next push so
// the skip stays bounded. A nil done cell (e.g. the bool under
// executionTracedKey) is never skipped.
func (s *contextStack) Peek(key any) any {
	if s == nil || *s == nil {
		return nil
	}

	stack := (*s)[key]
	for i := len(stack) - 1; i >= 0; i-- {
		e := stack[i]
		if e.done != nil && e.done.Load() {
			continue // finished/recycled: never surface as the active value
		}
		return e.value
	}
	return nil
}

// Push appends val to the stack under key and records done as the liveness
// cell for this activation.
//
// Before appending, Push drops any trailing entries whose done cell reports
// true — meaning the corresponding span or operation finished, possibly on a
// different goroutine whose goroutine-scoped popper was therefore a no-op. This
// bounds GLS growth in the cross-goroutine-finish pattern without reading any
// mutable flag off the (potentially recycled) value itself: the cell pointer in
// the stack entry is independent of the span, so pool reuse cannot flip it back.
//
// done may be nil for values that are never reclaimable (e.g. the bool stored
// under executionTracedKey); those entries are never drained.
func (s *contextStack) Push(key, val any, done *atomic.Bool) {
	if s == nil || *s == nil {
		return
	}

	stack := (*s)[key]
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		if top.done == nil || !top.done.Load() {
			break
		}
		top.value = nil // drop references so GC can collect both the value and the cell
		top.done = nil
		stack = stack[:len(stack)-1]
	}

	(*s)[key] = append(stack, stackEntry{value: val, done: done})
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
	val := stack[lastIdx].value
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

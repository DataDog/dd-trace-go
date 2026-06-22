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
// represent a live scope and may be dropped from the stack. It is used to
// bound GLS growth when a value is pushed on one goroutine but its lifecycle
// ends on another, so the matching pop never runs on the pushing goroutine
// (e.g. a *tracer.Span pushed via ContextWithSpan and finished elsewhere).
// The orchestrion package intentionally does not import the tracer; values
// opt in by implementing this single method.
type reclaimable interface {
	// GLSReclaimable reports whether this value is safe to drop from a GLS
	// stack. Implementations must be safe to call from any goroutine.
	GLSReclaimable() bool
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

// Peek returns the top context from the stack without removing it.
func (s *contextStack) Peek(key any) any {
	if s == nil || *s == nil {
		return nil
	}

	stack, ok := (*s)[key]
	if !ok || len(stack) == 0 {
		return nil
	}

	return (*s)[key][len(stack)-1]
}

// Push adds a context to the stack.
//
// Before appending, Push drops any trailing entries that report themselves
// reclaimable (see [reclaimable]). This bounds GLS growth when values are
// pushed on one goroutine but their lifecycle ends on another, so the pop
// never runs on this goroutine. Only the top of the stack is ever read (via
// Peek), and buried entries exist solely to be restored after a Pop; a
// reclaimable (e.g. finished) entry can never be a meaningful restore target,
// so dropping it preserves stack semantics. Entries whose type does not
// implement [reclaimable] (e.g. the bool stored under executionTracedKey) are
// never dropped.
func (s *contextStack) Push(key, val any) {
	if s == nil || *s == nil {
		return
	}

	stack := (*s)[key]
	for len(stack) > 0 {
		r, ok := stack[len(stack)-1].(reclaimable)
		if !ok || !r.GLSReclaimable() {
			break
		}
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

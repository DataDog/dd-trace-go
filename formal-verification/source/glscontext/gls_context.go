// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package glscontext extracts the GLS push/pop protocol from dd-trace-go's
// orchestrion integration. This file is the input to Specula's Go→TLA+ translation.
//
// The critical invariant: every Push(key, val) must have a corresponding Pop(key)
// on every code path, otherwise the GLS stack leaks and corrupts span parent
// resolution for subsequent operations on the same goroutine.
//
// This directly complements the kakkoyun/orchestrion_gls_leak branch work.
package glscontext

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// contextStack models the GLS slot stored in runtime.g by orchestrion.
// It holds context values shared within a single goroutine.
type contextStack map[any][]any

// ActiveSpanKey is the well-known key used for span storage in the GLS.
var ActiveSpanKey = struct{ name string }{name: "active-span"}

// Span is a minimal representation for the model.
type Span struct {
	ID       uint64
	Finished bool
}

// ---------------------------------------------------------------------------
// contextStack operations
// ---------------------------------------------------------------------------

// Peek returns the top value for key without removing it.
func (s *contextStack) Peek(key any) any {
	if s == nil || *s == nil {
		return nil
	}
	stack, ok := (*s)[key]
	if !ok || len(stack) == 0 {
		return nil
	}
	return stack[len(stack)-1]
}

// Push adds a value to the stack for the given key.
func (s *contextStack) Push(key, val any) {
	if s == nil || *s == nil {
		return
	}
	(*s)[key] = append((*s)[key], val)
}

// Pop removes and returns the top value for the given key.
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
	stack[lastIdx] = nil // allow GC
	stack = stack[:lastIdx]
	if len(stack) == 0 {
		delete(*s, key)
	} else {
		(*s)[key] = stack
	}
	return val
}

// ---------------------------------------------------------------------------
// Protocol: the paired push/pop lifecycle
// ---------------------------------------------------------------------------

// CtxWithValue models the push site (tracer.ContextWithSpan → orchestrion.CtxWithValue).
// Every call pushes the span onto the GLS stack for the current goroutine.
func CtxWithValue(stack *contextStack, key, val any) {
	stack.Push(key, val)
}

// GLSPopValue models the pop site (tracer.Span.Finish → orchestrion.GLSPopValue).
// This MUST be called on every code path that exits a span's scope.
func GLSPopValue(stack *contextStack, key any) any {
	return stack.Pop(key)
}

// ---------------------------------------------------------------------------
// Simulated span lifecycle (what Specula will model-check)
// ---------------------------------------------------------------------------

// StartSpan models the full push-side protocol:
//  1. Create span
//  2. Push onto GLS
func StartSpan(stack *contextStack, id uint64) *Span {
	s := &Span{ID: id}
	CtxWithValue(stack, ActiveSpanKey, s)
	return s
}

// FinishSpan models the full pop-side protocol:
//  1. Mark span finished
//  2. Pop from GLS
//
// The invariant: after FinishSpan, the stack depth for ActiveSpanKey
// must be exactly one less than before the call.
func FinishSpan(stack *contextStack, s *Span) {
	if s == nil {
		return
	}
	s.Finished = true
	GLSPopValue(stack, ActiveSpanKey)
}

// ---------------------------------------------------------------------------
// Invariants to verify (expressed as TLA+ temporal properties)
// ---------------------------------------------------------------------------
//
// INV1: PushPopPairing
//   ∀ goroutine g, key k:
//     #Push(g, k) = #Pop(g, k) when all spans on g are finished.
//   The stack for any key returns to empty when all work is done.
//
// INV2: StackDepthMonotonic
//   Between a Push and its corresponding Pop, the stack depth for that key
//   is strictly greater than before the Push.
//
// INV3: NoLeakOnFinish
//   ∀ span s: s.Finished = TRUE ⟹ the GLS entry pushed by StartSpan(s)
//   has been popped.
//
// INV4: PeekReturnsLatest
//   Peek(k) always returns the value from the most recent Push(k, v)
//   that has not been Popped.
//
// INV5: PopOrderLIFO
//   Pop(k) returns values in reverse order of Push(k, v) calls (stack semantics).

# Phase 2: GLS Push/Pop Model

## Overview

The GLS (Goroutine-Local Storage) push/pop model captures the protocol used by dd-trace-go's orchestrion integration to track active spans within a goroutine. Unlike the span lifecycle model (Phase 1), this model is **single-goroutine** — the GLS is goroutine-local by design. The key concern is ensuring balanced push/pop operations on every code path.

## Relationship to `kakkoyun/orchestrion_gls_leak`

This model directly complements the work on the `kakkoyun/orchestrion_gls_leak` branch, which addresses GLS context leaks. The formal verification can confirm that the fix ensures push/pop pairing under all scenarios, including error paths and deferred cleanup.

## Source Mapping

| Model Element | Source Location | Lines |
|--------------|-----------------|-------|
| `contextStack` type | `internal/orchestrion/context_stack.go` | 13 |
| `Push()` | `internal/orchestrion/context_stack.go` | 42-48 |
| `Pop()` | `internal/orchestrion/context_stack.go` | 51-74 |
| `Peek()` | `internal/orchestrion/context_stack.go` | 28-39 |
| `CtxWithValue()` (push site) | `internal/orchestrion/context.go` | 35-42 |
| `GLSPopValue()` (pop site) | `internal/orchestrion/context.go` | 48-54 |
| Push call in `ContextWithSpan()` | `ddtrace/tracer/context.go` | 18-19 |
| Pop call in `Span.Finish()` | `ddtrace/tracer/span.go` | 875 |

## The GLS Protocol

### How It Works

Orchestrion injects a goroutine-local storage slot into Go's `runtime.g` struct. This slot holds a `contextStack` — a `map[any][]any` that acts as a per-key stack.

The span lifecycle integrates with the GLS through two sites:

**Push site** — `ContextWithSpan()`:
```go
func ContextWithSpan(ctx context.Context, s *Span) context.Context {
    newCtx := orchestrion.CtxWithValue(ctx, internal.ActiveSpanKey, s)
    // ...
}
```

This calls `CtxWithValue`, which pushes the span onto the GLS stack:
```go
func CtxWithValue(parent context.Context, key, val any) context.Context {
    getDDContextStack().Push(key, val)
    return context.WithValue(WrapContext(parent), key, val)
}
```

**Pop site** — `Span.Finish()`:
```go
func (s *Span) Finish(opts ...FinishOption) {
    // ... finish logic ...
    s.finish(t)
    orchestrion.GLSPopValue(sharedinternal.ActiveSpanKey)
}
```

### Stack Semantics

The `contextStack` implements standard stack (LIFO) semantics per key:

```
Push(ActiveSpanKey, spanA)  → stack: [spanA]
Push(ActiveSpanKey, spanB)  → stack: [spanA, spanB]
Peek(ActiveSpanKey)         → spanB (latest)
Pop(ActiveSpanKey)          → spanB, stack: [spanA]
Pop(ActiveSpanKey)          → spanA, stack: []
```

This naturally supports nested spans: a child span is pushed after its parent and popped before its parent (since `Finish` pops and children finish before parents in well-structured code).

### The Leak Problem

If a code path calls `ContextWithSpan` (push) but does not call `Finish` (pop), the GLS entry leaks. Subsequent operations on the same goroutine will see a stale span as the "active" span, corrupting:

1. **Parent resolution**: New spans would incorrectly parent to the leaked span
2. **Stack depth**: The stack grows without bound over the goroutine's lifetime
3. **Context propagation**: `SpanFromContext` returns the wrong span

This is precisely what the `kakkoyun/orchestrion_gls_leak` branch fixes with `GLSPopFunc`:

```go
func GLSPopFunc(key any) func() {
    pushStack := getDDContextStack()
    return func() {
        if gls := getDDGLS(); gls != nil && gls.(*contextStack) == pushStack {
            pushStack.Pop(key)
        }
    }
}
```

## Invariants

### INV1: PushPopPairing

**Formal**: ∀ goroutine g, key k: when all spans on g are finished, `Len(stacks[g][k]) = 0`

**English**: When all spans on a goroutine have been finished, the GLS stack for every key is empty.

**Why it matters**: Prevents leak accumulation. After a request handler completes, no stale spans should remain.

### INV2: StackDepthMonotonic

**Formal**: Between a Push(k, v) and its corresponding Pop(k), the stack depth for k is strictly greater than before the Push.

**English**: A push always increases stack depth; a pop always decreases it.

**Why it matters**: Ensures the stack maintains structural integrity — no phantom entries or missing pushes.

### INV3: NoLeakOnFinish

**Formal**: ∀ span s: s.Finished ⟹ the GLS entry pushed by StartSpan(s) has been popped.

**English**: If a span is marked finished, its GLS entry no longer exists on the stack.

**Why it matters**: This is the primary invariant the `orchestrion_gls_leak` branch is trying to enforce.

### INV4: PeekReturnsLatest

**Formal**: `Peek(k) = Last(stacks[g][k])` when the stack is non-empty.

**English**: Peek always returns the most recently pushed value that has not been popped.

**Why it matters**: `SpanFromContext` uses Peek internally. If this invariant breaks, the wrong span is returned.

### INV5: PopOrderLIFO

**Formal**: Pop(k) returns values in reverse order of Push(k, v) calls.

**English**: The stack behaves as a stack (LIFO), not a queue (FIFO).

**Why it matters**: Nested span cleanup must happen in reverse order — child spans must be popped before parent spans.

## TLA+ Model Structure

The generated TLA+ specification models:

- **Variables**: `stacks` (function: goroutine → key → sequence of values), `active_spans` (set of started but not finished spans)
- **Actions**: `StartSpan(g, id)`, `FinishSpan(g, span)`, `Peek(g, key)`
- **Properties**: The 5 invariants above

The model uses 2 goroutines and 3 spans, sufficient to verify:
- Simple push/pop pairing (1 span per goroutine)
- Nested push/pop (2 spans on same goroutine)
- Concurrent goroutines with independent stacks

# Phase 1: Span Lifecycle Model

## Overview

The span lifecycle model captures the concurrency protocol governing `Span`, `SpanContext`, and `trace` types in dd-trace-go. These three types form a hierarchy where multiple goroutines concurrently create, modify, and finish spans within a shared trace buffer.

## Source Mapping

The extracted model in `source/span_lifecycle.go` corresponds to these locations in the dd-trace-go codebase:

| Model Element | Source Location | Lines |
|--------------|-----------------|-------|
| `Span` struct | `ddtrace/tracer/span.go` | 120-150 |
| `trace` struct | `ddtrace/tracer/spancontext.go` | 416-440 |
| `SetOperationName()` | `ddtrace/tracer/span.go` | 878-894 |
| `Span.finish()` | `ddtrace/tracer/span.go` | 896-961 |
| `trace.push()` | `ddtrace/tracer/spancontext.go` | 568-592 |
| `trace.finishedOneLocked()` | `ddtrace/tracer/spancontext.go` | 620-745 |

## The Concurrency Protocol

### Lock Hierarchy

```
span.mu (RWMutex)
  └── trace.mu (RWMutex)
```

All mutable span fields are protected by `span.mu`. Trace-level state (span buffer, finished count, sampling) is protected by `trace.mu`. The invariant is:

> If a goroutine holds both locks, it acquired `span.mu` first.

This is documented at `spancontext.go:617`:
```go
// Lock ordering: span.mu -> trace.mu. The caller holds s.mu.
```

### Finish-Guard Pattern

The `finished` flag on `Span` serves as a guard preventing modification after a span is submitted for flushing:

```go
func (s *Span) SetOperationName(operationName string) {
    s.mu.Lock()
    defer s.mu.Unlock()
    if s.finished {
        return  // guard: no modification after finish
    }
    s.name = operationName
}
```

This pattern is critical because **spans are not locked during flushing**. The `finished` flag is set while `span.mu` is held, and flushing only reads spans that have `finished=true`.

### Double-Finish Guard

At `spancontext.go:634`, a finished span's second `Finish()` call is a no-op:

```go
if s.finished {
    t.mu.Unlock()
    return
}
s.finished = true
```

This ensures idempotent finish behavior — important because the GLS pop in `Finish()` happens after `finish()` returns, and a concurrent double-finish must not corrupt the trace state.

### Partial Flush Lock Inversion

The most subtle concurrency point is the partial flush path at `spancontext.go:714-731`. When a partial flush is triggered:

1. `finishedOneLocked()` holds `s.mu` (caller) and acquires `trace.mu`
2. It identifies `fSpan` (the first span in the flush chunk)
3. If `fSpan != s`, it needs to lock `fSpan.mu` to set trace-level tags
4. **But**: acquiring `fSpan.mu` while holding `trace.mu` would violate lock ordering

The solution:
```go
// Release trace.mu BEFORE acquiring fSpan.mu
t.spans = leftoverSpans
t.finished = 0
t.mu.Unlock()

// Now safe to lock fSpan
if !currentSpanIsFirstInChunk {
    fSpan.mu.Lock()
    defer fSpan.mu.Unlock()
}
```

This is documented with reference to #incident-46344.

## Invariants

### INV1: NoModificationAfterFinish

**Formal**: ∀ s ∈ Spans: s.finished = TRUE ⟹ □(s.name = s.name')

**English**: Once a span's `finished` flag is set, no mutable field changes.

**Why it matters**: The tracer reads span fields without holding `span.mu` during flush. The `finished` flag establishes a happens-before relationship: flush only reads `finished=true` spans, and modification checks `finished` under lock.

### INV2: LockOrdering

**Formal**: ∀ g ∈ Goroutines: holds(g, trace.mu) ⟹ ¬holds(g, span.mu) ∨ acquired_span_mu_first(g)

**English**: A goroutine holding `trace.mu` either does not hold any `span.mu`, or acquired `span.mu` before `trace.mu`.

**Why it matters**: Prevents deadlock between concurrent span operations and trace-level operations.

### INV3: FinishIdempotent

**Formal**: ∀ s ∈ Spans: s.finished ⟹ finish(s) does not modify state.

**English**: Calling `Finish()` on an already-finished span is a no-op.

**Why it matters**: Prevents double-counting in `trace.finished` and double-submission.

### INV4: PartialFlushSafety

**Formal**: The lock inversion in partial flush does not introduce a deadlock cycle.

**English**: `trace.mu` is always released before `fSpan.mu` is acquired on the partial flush path.

**Why it matters**: This is the most complex concurrency interaction in the tracer. The #incident-46344 fix introduced this pattern, and formal verification can confirm it is correct for all interleavings.

### INV5: SamplingDecisionAtomicity

**Formal**: `samplingDecision` transitions are linearizable.

**English**: The `+checkatomic` annotation on `samplingDecision` requires that all reads/writes use atomic operations.

## TLA+ Model Structure

Specula will generate a TLA+ specification with:

- **Variables**: `spans` (sequence), `finished` (function: Span → Bool), `locks` (function: Goroutine → set of locks), `trace_state` (buffer, finished count, full flag)
- **Actions**: `StartSpan`, `SetOperationName`, `FinishSpan`, `PartialFlush`
- **Invariants**: The 5 properties above, expressed as TLA+ state predicates and temporal formulas

The model uses 2 goroutines and 3 spans by default, which is sufficient to expose most concurrency bugs while keeping the state space tractable for TLC.

# Background: Why Formal Verification for dd-trace-go

## The Problem

dd-trace-go's tracer manages complex concurrent state across goroutines. The core data structures — `Span`, `SpanContext`, and `trace` — are accessed concurrently by application goroutines (creating and finishing spans) and by the tracer's background goroutines (flushing, sampling, telemetry).

The correctness of this concurrent access currently relies on:

### 1. Code Comments

Lock ordering is documented in comments but not enforced statically:

```go
// Lock ordering: span.mu -> trace.mu. The caller holds s.mu.
// This function acquires t.mu.
func (t *trace) finishedOneLocked(s *Span) {
```

This is the **only** documentation of a critical invariant that prevents deadlock across the entire tracer. If a developer violates this ordering in a new code path, the comment does not prevent it.

### 2. Runtime Assertions

The `internal/locking/assert` package provides runtime lock-state checks:

```go
assert.RWMutexLocked(&s.mu) // panics if s.mu is not held
```

These are valuable but only fire when the specific code path is exercised. They cannot prove the invariant holds for **all** interleavings.

### 3. The `-race` Flag

Go's race detector finds data races on code paths exercised by tests. It does not find:
- Deadlocks (lock ordering violations that happen to not occur in test interleavings)
- Logic errors in the protocol (e.g., a span modification after finish that only manifests under specific goroutine scheduling)
- State-space coverage gaps (rare partial flush + concurrent finish timing)

### 4. `checklocks` (Currently Disabled)

The `checklocks` static analyzer from `gvisor.dev/gvisor/tools/checklocks` can verify lock annotations at compile time. However, the dd-trace-go CI job for `checklocks` is currently disabled. Only 2 annotations exist:

```go
mu locking.RWMutex `msg:"-"` // all fields are protected by this RWMutex
samplingDecision samplingDecision // +checkatomic
```

The `+checklocks:mu` annotation that would protect individual fields is not applied to most struct fields.

## What Formal Verification Adds

TLA+ model checking via Specula provides **exhaustive interleaving coverage** for modelled invariants. Unlike testing, which exercises specific schedules, TLC (the TLA+ model checker) explores every possible interleaving of every possible sequence of actions.

For dd-trace-go, this means we can mathematically verify:

1. **No deadlock** exists in the span.mu → trace.mu lock ordering, including the partial flush lock inversion
2. **The finish-guard pattern** prevents all post-finish modifications, not just the ones our tests happen to exercise
3. **GLS push/pop pairing** is maintained on every code path, not just the happy path tested in unit tests

## Scope Limitations

Formal verification is not a silver bullet:

- **Model fidelity**: The TLA+ model is an abstraction. If the model omits a relevant detail, the verification does not cover it.
- **State explosion**: TLC can only check small model instances (2-3 goroutines, 2-3 spans). This is usually sufficient because concurrency bugs are typically exposable with small configurations.
- **Maintenance cost**: The model must be updated when the concurrency protocol changes.

The approach is most valuable as a **one-time deep verification** of existing invariants, with periodic re-runs when the concurrency protocol is modified.

## Why Now

The `kakkoyun/orchestrion_gls_leak` branch work on GLS context leak prevention makes this a natural time to:

1. Formally verify the GLS push/pop pairing invariant
2. Document the span lifecycle concurrency protocol with mathematical precision
3. Establish tooling for future formal verification when the protocol evolves

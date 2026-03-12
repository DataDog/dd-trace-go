# Go Code Review: twmb/franz-go Integration

## Critical Issues

### 1. Race Condition in Client Struct
**Location:** `kgo.go:20-26`

`tracerMu` only protects the consumer group ID check while the tracer field is written during initialization without synchronization. Either document that tracer is immutable after initialization, or protect all tracer field access.

### 2. Unbounded Slice Growth
**Location:** `kgo.go:46`

`activeSpans` slice is initialized with 0 capacity and has no upper bound. In high-throughput consumer scenarios with large batch sizes, this could lead to excessive memory allocation. Consider pre-allocating with a reasonable capacity.

### 3. Missing Error Propagation
**Location:** `internal/tracing/tracing.go:104-106`, `internal/tracing/tracing.go:137-139`

Inject errors are logged but not returned, making debugging difficult. Silent failures in context propagation could cause distributed traces to break without any visible indication to the caller.

## Major Issues

### 1. Inconsistent Mutex Locking Pattern
**Location:** `kgo.go:62-69`

`finishAndClearActiveSpans` locks and unlocks inline, creating a brief window where other goroutines could modify activeSpans during iteration. Use `defer c.activeSpansMu.Unlock()` for safety and clarity.

### 2. Potential Memory Leak in Header Modification
**Location:** `internal/tracing/carrier.go:33-52`

The `Set` method creates a new slice and assigns it every time a header is added. For records with many headers and multiple Set calls, this could cause significant allocations.

### 3. Inefficient Header Lookup
**Location:** `internal/tracing/carrier.go:36-45`

Linear search through headers for each Set operation is O(n). For distributed tracing, 5+ headers are typically injected, making this O(n²) overall.

### 4. Silent Consumer Group ID Fetch Failures
**Location:** `kgo.go:130`

`GroupMetadata()` error is ignored. If this fails repeatedly, spans won't include the consumer group tag, potentially breaking DSM tracking without any indication.

### 5. Non-Idempotent Span Finishing
**Location:** `kgo.go:62-69`

`finishAndClearActiveSpans` finishes all spans but doesn't check if they're already finished. If called multiple times (e.g., PollFetches then Close), spans could be finished twice, leading to incorrect timing data.

## Minor Issues

| Issue | Location | Description |
|-------|----------|-------------|
| Missing `t.Helper()` | `kgo_test.go:36-39` | `topicName` function doesn't call `t.Helper()` |
| Magic number | `kgo_test.go:90` | 10-second timeout should be a named constant |
| Missing godoc | `tracing.go:86` | `ExtractSpanContext` is exported but lacks documentation |
| TODO comment | `kgo.go:34` | Should be tracked in issue or removed |

## Positive Observations

- Good test coverage with functional tests for produce, consume, errors, span lifecycle, DSM
- Clean abstraction through `Record` and `Header` interfaces
- Proper context propagation with `tracer.ContextWithSpan` and `tracer.SpanFromContext`
- DSM integration properly done with pathway extraction, injection, and Kafka lag tracking
- Clear package documentation in `internal/tracing`
- Consistent naming following dd-trace-go conventions

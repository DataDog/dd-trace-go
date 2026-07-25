# `internal/config`

This package is the **single source of truth** for initializing, reading, and updating tracer configuration.

## Migration guidelines

The registry has two layers:

- `RawDefinition` records one raw key's maximum source policy and telemetry
  policy.
- `ConsumerBinding` records consumer identity, raw keys, a sampling boundary,
  and optional environment-only narrowing.

Every raw definition has at least one binding, and bindings cannot name
unregistered keys. A key may have more than one binding. A binding may narrow a
stable-capable definition to environment only, but it cannot widen an
environment-only definition.

Choose the boundary that preserves existing consumer behavior: package
initialization, tracer construction, product start, constructor, first use, or
per call. One key may intentionally have different boundaries for different
consumers. Tracer-owned configuration uses a generation; other products and
constructor-scoped users use lifecycle snapshots.

## Product claims and tracer generations

Programmatic configuration shared by the tracer and profiler is coordinated by
product claims. Tracer options apply to an unpublished generation and identify
the tracer as the claimant:

```go
cfg := internalconfig.NewTracerGeneration()
cfg.SetServiceName(name, internalconfig.OriginCode, internalconfig.ProductTracer)
prepared := cfg.PrepareClaims()
```

`NewTracerGeneration` creates an unpublished candidate. Call `PrepareClaims`
before constructing dependent components. `PublishTracerGeneration` rechecks
claims, atomically publishes, runs the synchronous handoff outside the store
lock, and retires the predecessor. If construction is abandoned, the
unpublished candidate does not affect the current generation. A publication
call that returns an error also leaves it unchanged. Once the candidate commits,
a panic during the synchronous handoff does not roll back the publication, and
predecessor retirement still occurs.

Other products acquire their claims with `AcquireProductClaims`. Matching claim
values may coexist; a different value is rejected. Each successful acquisition
returns an independent release function, and stale releases cannot remove newer
claims.

`AcquireProductClaims` reports conflicts before it returns. A product that must
publish its runtime state while holding its own lock uses
`PrepareProductClaims`, publishes and unlocks, and then invokes the returned
idempotent conflict reporter. This keeps telemetry callbacks outside the
product lock without delaying claim acquisition.

Rules:

- **Env vars, defaults, and RC always pass through** — claims apply only to programmatic `OriginCode` values.
- **Tracer customer options are product-bound** — shared setters must receive `ProductTracer`; omitting the product is reserved for internal initialization, tests, and integrations that intentionally bypass product ownership.
- **Prepare before use, publish at handoff** — never expose a staged generation or let dependent components consume it before conflicts are restored.
- **`Get` follows the current published non-nil generation** — code that should track tracer restarts calls `Get`. Running tracers and late Remote Config callbacks stay pinned to their generation.
- **Same-value claims may be shared** — conflicting values are first-in-wins. Conflict reporting occurs outside locks and includes only the claim name and product identities in telemetry and logs.

Current cross-product acquisition supports profiler claims; unsupported product
claims are rejected.

## Hot paths & performance guidelines

Some configuration accessors may be called in hot paths (e.g., span start/finish, partial flush logic).
If benchmarks regress, ensure getters are efficient and do not:

- **Copy whole maps/slices on every call**: prefer single-key lookup helpers like `ServiceMapping`/`HasFeature` over returning a map copy.
- **Take multiple lock/unlock pairs to read related fields**: prefer a combined getter under one `RLock`, like `PartialFlushEnabled()`.
- **Rethink `defer` in per-span/tight-loop getters**: avoid `defer` in getters that are executed extremely frequently.

### Cache config reads before loops (especially retry loops)

If you’re reading a config value inside **any** loop, prefer caching it once into a **local variable** before the loop:

- **Why**: avoids repeated `RLock/RUnlock` overhead per iteration and keeps loop bounds/logging consistent if the value ever becomes dynamically updatable.
- **Example**: cache `SendRetries()` and `RetryInterval()` once per flush send, and use the cached values inside the loop.

```go
sendRetries := cfg.SendRetries()
retryInterval := cfg.RetryInterval()
for attempt := 0; attempt <= sendRetries; attempt++ {
	// ...
	time.Sleep(retryInterval)
}
```

### Snapshot many-field hot paths under one lock

When a hot path reads ~3+ `Config` fields, define a snapshot struct + method in `snapshots.go` and have the caller read from the local copy.

- **Why**: at high concurrency the bottleneck isn't blocking — readers don't block each other — but cache-line contention on `sync.RWMutex`'s reader counter. Folding N `RLock` pairs into 1 collapses N atomic ops on a shared cache line into 1.
- **Convention**: one bespoke struct per caller (e.g, a calling function `StartSpan` gets a snapshot API called `SpanStartSnapshot`).
- **Prior art**: `SpanStartSnapshot` for `tracer.StartSpan` (13 → 1 RLock acquisitions, ~60% speedup on `BenchmarkStartSpanConcurrent-8`).

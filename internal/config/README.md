# `internal/config`

This package is the **single source of truth** for initializing, reading, and updating tracer configuration.

## Migration guidelines

When migrating a configuration value from another package (e.g. `ddtrace/tracer`):

- **Define the field on `Config`**: add a private field on `internal/config.Config`.
- **Initialize it in `loadConfig()`**: read from the config provider, which iterates over the following sources, in order, returning the default if no valid value found: local declarative config file, OTEL env vars, env vars, managed declarative config file
- **Expose an accessor**: add a getter (and a setter if the value is updated at runtime).
- **Report telemetry in setters**: setters should call `configtelemetry.Report(...)` with the correct origin.
- **Add the cross-product gate**: every product-bound setter must call `c.checkProductConflict(...)` as its first action after acquiring the lock (see below).
- **Update callers**: replace reads/writes on local "config" structs with calls to the generation owned by that product. Use `internal/config.Get()` only when the caller intentionally follows the currently published generation.
- **Delete old state**: remove the migrated field from any legacy config structs once no longer referenced.
- **Update tests**: tests should call the singleton setter/getter (or set env vars) rather than mutating legacy fields.

Sample migration PR: https://github.com/DataDog/dd-trace-go/pull/4214

## Product claims and tracer generations

Programmatic configuration shared by the tracer and profiler is coordinated by
product claims. Tracer options apply to an unpublished generation and identify
the tracer as the claimant:

```go
cfg := internalconfig.NewTracerGeneration()
cfg.SetServiceName(name, internalconfig.OriginCode, internalconfig.ProductTracer)
prepared := cfg.PrepareClaims()
```

`PrepareClaims` snapshots the staged claims and restores source-resolved values
for claims that already conflict. Construct components that consume
configuration only after preparation. At the tracer handoff,
`PublishTracerGeneration` atomically rechecks for claim races, publishes the
generation, and replaces the previous tracer's claims. A failed construction or
publication does not change the current generation or its claims.

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
- **`Get` follows the current generation** — code that should track tracer restarts calls `Get`. A running tracer keeps its own pinned `*Config`, so retiring a generation does not invalidate its reads or late RC updates.
- **Same-value claims may be shared** — conflicting values remain first-in-wins and emit only claim name and product identities in telemetry and logs.

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

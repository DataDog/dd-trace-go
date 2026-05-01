# `internal/config`

This package is the **single source of truth** for initializing, reading, and updating tracer configuration.

## Migration guidelines

When migrating a configuration value from another package (e.g. `ddtrace/tracer`):

- **Define the field on `Config`**: add a private field on `internal/config.Config`.
- **Initialize it in `loadConfig()`**: read from the config provider, which iterates over the following sources, in order, returning the default if no valid value found: local declarative config file, OTEL env vars, env vars, managed declarative config file
- **Expose an accessor**: add a getter (and a setter if the value is updated at runtime).
- **Report telemetry in setters**: setters should call `configtelemetry.Report(...)` with the correct origin.
- **Add the cross-product gate**: every setter must call `c.checkProductConflict(...)` as its first action after acquiring the lock (see below).
- **Update callers**: replace reads/writes on local "config" structs with calls to the singleton (`internal/config.Get()`).
- **Delete old state**: remove the migrated field from any legacy config structs once no longer referenced.
- **Update tests**: tests should call the singleton setter/getter (or set env vars) rather than mutating legacy fields.

Sample migration PR: https://github.com/DataDog/dd-trace-go/pull/4214

## Cross-product gate

Every `Set*` method accepts an optional trailing `...Product` parameter. When a product (tracer, profiler, etc.) sets a field via its programmatic API, it passes its `Product` identity:

```go
c.internalConfig.SetServiceName(name, internalconfig.OriginCode, internalconfig.ProductTracer)
```

The gate enforces **first-in-wins**: if a different product already claimed the field via programmatic API, the call is silently rejected and a warning is logged. This prevents conflicting overrides like `tracer.WithService("A")` + `profiler.WithService("B")`.

Key rules:
- **Env vars, defaults, and RC always pass through** — the gate only activates for `OriginCode`.
- **Tests and integrations omit the product** — they call `SetServiceName(name, origin)` without a product, bypassing the gate entirely.
- **Same product can call a setter multiple times** — repeated calls from the same product just update the value.

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


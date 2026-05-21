# AGENTS.md — `internal/config` migrations

This doc captures the implicit rules for migrating configuration into `internal/config` — the things the code doesn't make obvious. For source priority, gate semantics, and the `DynamicConfig` API, read `provider/provider.go` and `dynamic_config.go` directly.

## Scope rule: one field per migration, dependencies first

Before starting, trace every config the target depends on transitively. If any upstream config is still owned by the legacy package, **stop and surface to the user**:

> "X depends on Y, which is still owned by `ddtrace/tracer`. Migrate Y first?"

Never silently expand scope. Never migrate a derived field before its base — even if the base looks trivial. The base goes in a separate prior PR.

## Minimalism: add only what's used

- No setter unless a caller invokes it.
- No `*DynamicConfig[T]` accessor unless something outside this package needs RC handling. `globalSampleRate` doesn't expose one.
- No new provider method unless this migration needs it.
- Resist adding methods "for symmetry." Symmetry can be added later when there's a caller.
- Comments stay minimal. Keep a field's existing definition comment if it had one; for other comments (existing or new), only migrate or write them when they capture motivation that can't be gleaned from the code alone.

## Source of truth: no shadow state

`internal/config` is canonical. Once a field is migrated, every read and write of that field goes through `internal/config`.

## Testing

Rely on existing tests in the source package for regression coverage. Add tests in `internal/config` only when this migration introduces new functionality there.

## Trace every runtime write

Before adding a setter for a runtime write, trace the written value to its literal source (env var, file, function input) and grep `internal/config/` for that source. If `internal/config` already reads it, the write is redundant — refactor the caller to read from `internal/config` (via the getter), eliminate the write, no setter needed.

## Migration plan

Produce a plan before writing code. The plan lists:

- **Read sites**: one line per site (`file:line → new accessor`).
- **Write sites**: for each site, the value expression; the traced literal source (`file:line` + env var / function input); the `grep` result against `internal/config/`; and the resulting action (redirect to read from `internal/config`, or add a setter).
- **Surface added to `internal/config`**: getter only, getter + setter, etc.

The `grep` output makes the "trace every runtime write" check auditable at a glance — no need to chase the value source through the codebase.

## Migration recipes

Focus on non-obvious bits. Defer to the reference PRs for code shape.

If the field has a `WithX` option in the product package, the migration also rewrites that option to delegate:

```go
c.internalConfig.SetX(val, telemetry.OriginCode, internalconfig.ProductX)
```

`ProductX` matches the calling product — `ProductTracer`, `ProductProfiler`, etc.

### A. Static config field — see #4214

The basic shape: private field on `Config`, initialized in `loadConfig()` via the provider, getter (and setter if updated at runtime).

### B. Dynamic config field — see #4760

- Field type is `*DynamicConfig[T]`. Use `setBaseline`; **never reassign the pointer** — it would orphan RC subscribers.
- The provider needs `GetXWithOrigin` for the underlying type. Add it if absent.
- Expose the `*DynamicConfig[T]` via an `XConfig()` accessor only if a caller actually needs to drive RC updates or read the baseline origin.

## Chip away during every migration

- `ddtrace/tracer/telemetry.go:startTelemetry` already carries a TODO for full deletion (APMAPI-1771). Its `telemetryConfigs` slice lists each field the tracer explicitly reports on startup; `configtelemetry` inside `internal/config` already reports the field automatically from the setter. **Remove the migrated field's line from that slice in the same PR.**
- `globalconfig` is targeted for full deletion, but it's a cross-package shared store: a field can only be removed once *all* packages reading or writing it have migrated. Don't add to it. When you migrate the last caller for a given field, remove that field from `globalconfig` in the same PR.

## Hot path notes

See `README.md` — the hot-path conventions (cache reads before loops, snapshot many-field hot paths) apply to migrated code too.

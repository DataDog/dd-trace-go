---
name: config-migration
description: >
  Migrates configuration fields still stored in other packages (such as ddtrace/tracer,
  profiler, or globalconfig) into internal/config in the dd-trace-go repository. Use when moving
  a field into internal/config, even if the request doesn't say "migrate" (e.g. replacing a
  package's own env-var read or config-struct field with an internal/config accessor), or when
  producing a migration-plan PR. Not for edits to internal/config that don't move a field from
  another package. Covers scope-and-dependency rules (dependencies first, no silent expansion),
  the no-shadow-state contract, minimalism for accessors and setters, tracing every runtime write
  to its literal source, static-config vs DynamicConfig recipes, the migration-plan format
  required before writing code, and what to remove from globalconfig and the source package's
  manual telemetry reporting in the same PR.
---

# config-migration

Rules for migrating configuration fields into `internal/config` in the dd-trace-go repository — the things the code doesn't make obvious. For source priority, gate semantics, and the `DynamicConfig` API, read `internal/config/provider/provider.go` and `internal/config/dynamic_config.go` directly.

## Scope rule: dependencies first

Before starting, trace every config the target depends on transitively. If any upstream config is still stored in its original package, **stop and surface to the user**:

> "X depends on Y, which is still owned by `ddtrace/tracer`. Migrate Y first?"

Never silently expand scope. Never migrate a derived field before its base — even if the base looks trivial.

## Minimalism: add only what's used

- No setter unless a caller invokes it.
- No `*DynamicConfig[T]` accessor unless something outside this package needs RC handling.
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

### A. Static config field — see PR #4214

The basic shape: private field on `Config`, initialized in `loadConfig()` via the provider, getter (and setter if updated at runtime).

### B. Dynamic config field — see PR #4760

- Field type is `*DynamicConfig[T]`. Use `setBaseline`; **never reassign the pointer** — it would orphan RC subscribers.
- The provider needs `GetXWithOrigin` for the underlying type. Add it if absent.
- Expose the `*DynamicConfig[T]` via an `XConfig()` accessor only if a caller actually needs to drive RC updates or read the baseline origin.

## Chip away during every migration

- Source packages report their config to telemetry by hand (e.g. `startTelemetry` in `ddtrace/tracer` or `profiler`, `registerTelemetry` in `ddtrace/opentelemetry/log` or `ddtrace/opentelemetry/metric`); `internal/config` already reports migrated fields automatically. **Remove the migrated field's manual reporting from the source package in the same PR.**
- `globalconfig` is targeted for full deletion, but it's a cross-package shared store: a field can only be removed once *all* packages reading or writing it have migrated. Don't add to it. When you migrate the last caller for a given field, remove that field from `globalconfig` in the same PR.

## Hot path notes

The hot-path conventions in [`internal/config/README.md`](/internal/config/README.md) (cache reads before loops, snapshot many-field hot paths) apply to migrated code too.

## Before opening the PR

Run the `config-migration-review` skill against your local diff for a reviewer-perspective self-check.

**BEFORE writing or editing ANY code**, you MUST read [README.md](./README.md) for the migration steps, the cross-product gate, and hot-path guidelines.

## Skills

* `config-migration` ([SKILL.md](/.agents/skills/config-migration/SKILL.md)) -- use when adding a field to `internal/config` or migrating one from a legacy package (e.g. `ddtrace/tracer`, `globalconfig`). Produce its migration plan (read sites, write sites, and the surface added to `internal/config`) before writing code.
* `config-migration-review` ([SKILL.md](/.agents/skills/config-migration-review/SKILL.md)) -- use when reviewing a diff that touches `internal/config`, including self-review before opening a PR.

## Rules

* `internal/config` is the single source of truth. Once a field is migrated, every read and write goes through the `internal/config.Get()` singleton; no env-var parsing, defaults, or copies of the value remain in the legacy package.
* Migrate dependencies first. If a field depends on a config still owned by a legacy package, stop and ask the developer to migrate that dependency first; never migrate a derived field while its base stays in the legacy package.
* Add only what has a caller: no setter unless something writes the field at runtime, no `*DynamicConfig[T]` accessor unless a caller needs RC handling.
* Setters have the shape `SetX(value, origin, product...)` and call `c.checkProductConflict(...)` first after acquiring the lock. Additive collection fields (feature flags, service mappings) merge across products and are not gated.
* Getters for map/slice fields return a copy, never the internal reference.
* When a field is migrated, remove its entry from `telemetryConfigs` in `ddtrace/tracer/telemetry.go`; the setter already reports it. Never add fields to `globalconfig`; remove a field from it when its last caller is migrated.
* Keep comments minimal: only add one to express intent that the code alone doesn't make clear.

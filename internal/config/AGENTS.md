**BEFORE writing or editing ANY code**, you MUST read [README.md](./README.md) for the migration steps, the cross-product gate, and hot-path guidelines.

The rules for working on this package live in two skills. Read the applicable one in full before acting:

* [`config-migration`](/.agents/skills/config-migration/SKILL.md) -- you MUST read this before moving a config field that is still stored in another package (e.g. `ddtrace/tracer`, `profiler`, `globalconfig`) into `internal/config`, whether or not the request says "migrate". This includes replacing that package's own env-var read, default, or config-struct field with an `internal/config` accessor.
* [`config-migration-review`](/.agents/skills/config-migration-review/SKILL.md) -- you MUST read this when reviewing a diff that touches `internal/config/`, including self-review before opening a PR.

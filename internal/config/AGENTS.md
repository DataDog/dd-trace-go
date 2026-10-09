**BEFORE writing or editing ANY code**, you MUST read [README.md](./README.md) for the migration steps, the cross-product gate, and hot-path guidelines.

The rules for working on this package live in two skills. Read the applicable one in full before acting:

* [`config-migration`](/.agents/skills/config-migration/SKILL.md) -- you MUST read this before editing any file in `internal/config/`, or when asked to add a config field or migrate one from a legacy package (e.g. `ddtrace/tracer`, `globalconfig`), even if no file in this package has been opened yet.
* [`config-migration-review`](/.agents/skills/config-migration-review/SKILL.md) -- you MUST read this when reviewing a diff that touches `internal/config/`, including self-review before opening a PR.

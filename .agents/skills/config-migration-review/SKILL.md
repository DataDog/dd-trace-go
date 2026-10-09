---
name: config-migration-review
description: >
  Self-review checklist for a config-migration diff in dd-trace-go, before opening the PR.
  Use when asked to review local config-migration changes, "review my config migration,"
  or check a diff touching internal/config against recurring reviewer feedback. Companion
  to the config-migration skill (which covers authoring); this one covers the review-specific
  detection angle — what smell to grep for in a diff — for comment minimalism, Set<X> API
  shape and cross-product conflict-gate correctness, getter copy semantics, no-shadow-state
  completeness, test placement, stale telemetry cleanup, scope discipline, env-var/WithX
  parity, and hot-path caching. Also usable by a third-party reviewer who has checked out a
  contributor's branch locally.
---

# config-migration-review

Reviewer-perspective checklist for a config-migration diff, distilled from recurring feedback
across merged `refactor(config)`/`refactor(internal/config)` PRs. For the underlying rules this
checks against, see the `config-migration` skill — this skill states only the detection angle
and doesn't repeat that prose.

## How to run it

Determine the diff to review:

- Branch with commits: `git diff origin/main...HEAD` (or the repo's configured base branch).
- Uncommitted work: `git diff` / `git status`.

This also works as a third-party reviewer: check out the contributor's branch locally first,
then run the same diff command — it doesn't matter whose branch is checked out.

Walk the diff hunk-by-hunk against the checklist below.

## Checklist

In priority order — highest-value, most-repeated findings first.

### Comment minimalism

By far the most repeated theme. Flag:

- A new or modified comment that restates what the code already makes obvious.
- A doc comment longer than one short sentence.
- A comment narrating "why this changed" for a behavior fix, instead of just fixing the
  condition.
- Inconsistent comment presence across sibling fields (e.g. only one field in a set gets a
  description).

Past review feedback: *"I find the long winded comments more harmful than helpful... avoid
adding new comments, unless you need to express intention that is not clear from the code
alone."*

Blocking if it obscures a real defect; otherwise a nit.

### `Set<X>` shape and conflict-gate correctness

Flag:

- A new setter not matching `Set<X>(value, origin, product...)`.
- A single-source-of-truth field's setter missing the cross-product conflict gate.
- An *additive* collection field (feature flags, service mappings) incorrectly gated as if it
  were exclusive — additive fields merge across products, they don't conflict.
- `origin` redesigned to be variadic.
- Telemetry reported under a made-up key instead of the env-var-derived canonical name.

Blocking — this is a correctness/API-shape issue, not style.

### Getter copy semantics

Flag a new getter for a map/slice field that returns the internal reference/pointer directly
instead of a copy, breaking convention with existing getters — unless the field is a
`DynamicConfig[T]` with its own safe-replace semantics.

Blocking — a shared mutable reference leaking out of `internal/config` reintroduces the shadow
state the migration is meant to remove.

### Migration completeness / no shadow state

References `config-migration`'s "Source of truth" rule. Flag:

- Env-var reads, parsing, or precedence logic for the migrated field still present outside
  `loadConfig`/`internal/config`, despite the PR claiming the field is migrated.
- A new helper file duplicating provider-read logic instead of calling `p.GetXWithOrigin`
  inline in `loadConfig`, per existing pattern.

Blocking.

### Test placement

References `config-migration`'s "Testing" rule. Flag new test cases added under
`internal/config` for a field that already has regression coverage in the source package, when
the PR doesn't introduce new validator/helper logic there. Nit.

### Stale telemetry cleanup

References `config-migration`'s "Chip away" section. Flag:

- The migrated field's manual reporting still present in `ddtrace/tracer/telemetry.go`'s
  `startTelemetry`/`telemetryConfigs`, when the provider now auto-reports it.
- A leftover explanatory comment on an already-established deletion pattern.

Blocking (dead/duplicate telemetry) unless trivially cosmetic.

### Scope discipline

References `config-migration`'s "Scope rule". Flag any file or package touched outside the
migration's declared target fields or package, even if the edit looks correct in isolation.
Blocking — scope creep, not a defect in the edit itself.

### Env var / WithX parity

Extends `config-migration`'s "recipes" section. Flag:

- A field with a `WithX(...)` option that doesn't get an env var counterpart wired in the same
  migration, when one is plausible.
- Missing registration of a new env var in `internal/env/supported_configurations.json` and the
  dd-go telemetry config-norm-rules registry.

Blocking if the env var is user-facing and silently missing; otherwise a nit to flag the gap.

### Hot path / loop caching

References `config-migration`'s "Hot path notes". Flag a `Config`/`DynamicConfig` getter called
as a loop's range expression, or called live on a per-span/hot path, instead of being captured
once or added to a snapshot struct (e.g. `SpanStartSnapshot`). Blocking if genuinely hot path;
nit if the path is cold.

## Output format

Report findings as a flat list, review-comment style, grouped loosely by theme:

```
internal/config/config.go:142 — [blocking] SetGlobalTags missing cross-product conflict gate
ddtrace/tracer/option.go:88 — [nit] comment restates what SetX already says in its name
```

No scoring rubric, no severity taxonomy beyond `blocking`/`nit`.

# Source-Tagged Undo POC — Design Notes

Reference document for review of the source-tagged undo mechanism introduced in
PR #4692. This file is intentionally not for merge — it exists as a companion
to the PR while the design is being reviewed. Delete before merge (or move to
an architecture doc if useful long-term).

## 1. What we introduced

A source-tagged override + undo mechanism on the shared `internal/config`
singleton, plus an in-place env refresh path. Concretely:

- **`programmaticOverride` extended** with a `restore func()` closure, captured
  once per (field, product) on the first code-origin write.
- **`snapshotAndCheck`** — the write-time gate that enforces
  first-product-wins conflict semantics AND records the restore baseline.
  Legacy setters keep using the old `checkProductConflict` as a thin wrapper
  that skips baseline capture.
- **`undoProduct` / `refreshFromEnv`** (unexported methods) — internal
  primitives. `undoProduct` iterates the overrides map and calls `restore()`
  for entries matching the given product, then deletes them. `refreshFromEnv`
  re-reads env-backed values via `loadEnvInto`.
- **`(c *Config) PrepareForStart(Product)`** — the single Start-time entry
  point for products. Wraps `undoProduct + refreshFromEnv` as an opaque
  ceremony on an already-fetched Config. Contribs and other read-only
  consumers call `Get()` alone; products call `Get()` + `PrepareForStart()`
  at the top of their Start. The primitives are unexported to keep the
  product-facing surface narrow and prevent products from invoking only
  part of the ceremony.
- **`loadEnvInto`** — factored the env-reading portion of `loadConfig` into a
  method guarded by the overrides map, shared by initial load and refresh.
- **`CreateNew` removed** — the sledgehammer that rebuilt the whole singleton
  is gone; tracer's `newConfig` now uses `PrepareForStart(ProductTracer)`.
- **`SetEnv` migrated** as the POC setter demonstrating full undo + refresh
  participation.
- **`profiler.WithEnv` migrated** to route through the singleton via
  `cfg.internalConfig.SetEnv(...)`, and `profiler.defaultConfig` uses the same
  `PrepareForStart` call as tracer.

## 2. Why / toward what goal

The migration to a shared Config singleton in dd-trace-go kept running into
the same problem: supporting `tracer.Start(...)` being called more than once
("last options win") without wiping code-origin overrides from other products
like the profiler. The existing approach — `CreateNew()` rebuilding the
singleton on every tracer `Start` — solved the env-refresh case but forced
every product into its own isolated Config instance, which broke cross-product
conflict detection and caused drift between products' views of shared fields
like service/env/version.

The goal was a single shared Config that could be re-initialized on a
per-product basis, with env refresh on restart, without any product's restart
affecting another's overrides.

## 3. Why this was the right solution

We explored alternatives and ruled them out in order:

- **Per-product Config instances (current `CreateNew` model)**: causes drift
  and loses write-time conflict detection. The whole motivation for migrating
  to a singleton.
- **Base config + per-product overlays** (colleague's proposal): either
  requires API-break ("clean overlay") or reintroduces read-time merge
  ambiguity without resolving the underlying conflict-detection problem
  ("messy overlay"). And in the version where cross-cutting fields still flow
  through every product's API, you get no shared telemetry key — a structural
  flaw.
- **Tracer-only `CreateNew`**: pragmatic escape hatch, but asymmetric (two
  different Start semantics across products) and still wipes other products'
  overrides on repeat tracer Start.

Source-tagged undo + in-place env refresh via `PrepareForStart` gives you everything at once:
one singleton pointer shared across all products, write-time conflict
detection, per-product last-Start-wins, env refresh on restart, and no wipe of
other products' overrides. The mechanism extends existing primitives
(`overrides` map, `DynamicConfig[T]` baseline pattern) rather than introducing
a new abstraction.

## 4. How this unblocks us moving forward

- **Unblocks further field migration.** The mechanism is in place. Every
  subsequent setter migration is a one-function diff: capture `priorVal`,
  build a silent restore closure, call `snapshotAndCheck` instead of
  `checkProductConflict`. Add a corresponding guarded line in `loadEnvInto`
  for env-backed fields. No further design work per field.
- **Unblocks adding new products to the singleton.** Any new product (appsec,
  llmobs, CI vis, …) now has a clear Start-time pattern at the top of its
  Start: `c := internalconfig.Get(); c.PrepareForStart(<self>)`, followed by
  option application. Contribs and any read-only consumer only need `Get()`.
  Conflict detection with existing products is automatic.
- **Unblocks removing the `CreateNew` / `SetUseFreshConfig` sledgehammers.**
  `CreateNew` is gone. `SetUseFreshConfig` remains as a test helper but is no
  longer load-bearing for production.
- **Cleans up the "whack-a-mole" pattern** from earlier migration work. Each
  new field had been surfacing fresh cross-cutting questions (env refresh,
  restart, conflict). Those now have one answer each instead of one-per-field.
- **Known follow-ups, deliberately deferred:**
  - (a) Per-product `initialized atomic.Bool` to skip the redundant refresh on
    cold first-Starts — `TODO(perf)` in `refreshFromEnv`.
  - (b) Remote-config interaction with the new override/baseline layer.
  - (c) Expanding telemetry reporting in `loadEnvInto` to non-DD_ENV fields as
    they migrate to the new undo path.

  None of these block merging the POC.

## Scope reminder

- One singleton setter (`SetEnv`) is on the new undo path. The other 27
  setters still use `checkProductConflict`; they retain ownership tracking but
  do not participate in undo until individually migrated.
- One profiler option (`WithEnv`) is routed through the singleton. Others
  stay local.
- No RC integration.

## Follow-up: split commits for easier review

Commit 2 (`feat(config): in-place env refresh, PrepareForStart, remove
CreateNew`) exceeds GitHub's single-file diff-render threshold for
`internal/config/config.go`. Reviewers see "Diff is too big to render" in
the Files-changed tab.

The fix is to split commit 2 into four smaller, single-concern commits. This
does not change the end state of the branch; it just makes the Commits tab
walkable piece by piece.

Target split (apply with interactive rebase, `edit` the original commit 2,
then commit the four pieces in order):

**2a. Pure refactor: extract `loadEnvInto` from `loadConfig`**
- Move the inline env reads in `loadConfig` into a new `(c *Config)
  loadEnvInto()` method. `loadConfig` calls it.
- No guards, no new exported API, no renames.
- Reviewable as "this is just a code move."

**2b. Add override guards + DD_ENV telemetry inside `loadEnvInto`**
- Wrap each field write in `if _, ov := c.overrides["..."]; !ov { ... }`.
- Add `envOrigin(p, key)` helper.
- Add the one `configtelemetry.Report("DD_ENV", ...)` call in `loadEnvInto`
  using `envOrigin(p, "DD_ENV")`.
- Reviewable as "the guards are the behavior change; telemetry wires up
  for the single migrated field."

**2c. Add `refreshFromEnv` + `PrepareForStart`; unexport `undoProduct`**
- Add `(c *Config) refreshFromEnv()` (locks + calls `loadEnvInto`).
- Add `(c *Config) PrepareForStart(Product)` (wraps `undoProduct +
  refreshFromEnv`).
- Rename `UndoProduct` to `undoProduct` (unexport). Update commit 1's
  `TestUndoProduct` call sites to lowercase.

**2d. Remove `CreateNew`; wire tracer to `PrepareForStart`**
- Delete `CreateNew` function.
- `ddtrace/tracer/option.go`: `newConfig` does `Get + PrepareForStart`.
- Introduce `newFreshInternalConfig` helper in `tracer_test.go`; use it in
  `transport_test.go` and `span_to_otlp_test.go` where `CreateNew` was
  previously called.
- Drop `TestGet/GetNew forces new instance` subtest.

If commit 1 also hits the diff threshold after splitting commit 2 makes it
the visible offender, split it the same way:
- **1a.** Mechanism: extend `programmaticOverride` with `restore`, add
  `snapshotAndCheck`, keep `checkProductConflict` as a thin wrapper.
- **1b.** Add `UndoProduct` method (originally exported, unexported in 2c).
- **1c.** Migrate `SetEnv` to the new path + add `TestUndoProduct`.

Procedure (when ready to split):

```sh
git rebase -i 8c4fe256b   # commit 1 hash (before commit 2)
# mark commit 2 as `edit`, save, exit
git reset HEAD~1          # now commit 2's changes are in the working tree, unstaged
# commit the four pieces in order using `git add -p` or file-scoped staging
git rebase --continue
git push --force-with-lease
```

The end-state tree must match pre-split — diff the rebased branch against
the original tip (before `--force-with-lease`) and expect empty output.

---
name: migrate-orchestrion-to-otelc
description: >-
  Port a dd-trace-go integration's compile-time auto-instrumentation from Orchestrion to otelc
  (OpenTelemetry Go compile-time instrumentation). Use when an integration already has an
  orchestrion.yml and you need equivalent otelc rules plus hooks that produce the same spans.
---

# Migrate an integration from Orchestrion to otelc

Reproduce what the integration's `orchestrion.yml` does, with otelc rules and hooks that call the
existing contrib entrypoints. Do not reimplement tracing, and do not introduce mechanisms the
`orchestrion.yml` does not use.

**Scope:** work only in the current branch/worktree and the sources in `references.md`. Do not
inspect, borrow from, or depend on other branches, PRs, or worktrees, even if they contain otelc
work.

## Success criterion

The integration's existing tests under `internal/orchestrion/_integration/<name>/` pass built with
otelc instead of Orchestrion, with the same spans.

**Do not edit existing tests or assertions to make the migration pass.** Adding is fine and often
necessary: new cases, and stronger assertions on behaviour that was real but never pinned down
("exactly one span" usually needs a new assertion rather than an edited one).

For each behaviour difference you think you found, in this order:

1. Assert the orchestrion behaviour.
2. Run it under orchestrion. It must pass; if it does not, your reading of the current behaviour is
   wrong.
3. Run it under otelc. A difference argued from the call graph and never run is a guess.

Put each test at the lowest level that can still fail. A `contrib/<name>` unit test with
`mocktracer` covers most migrations; use `_integration` only when the assertion needs a woven build.
Check that each test fails when you break what it guards.

Two rungs, both run from `internal/orchestrion/_integration`:
- **Compile + inject:** `otelc go build ./<name>/...`.
- **Span parity:** `otelc go test ./<name>/...` against the same suite under `orchestrion go test`.

Rules with `target: main` fire in neither rung: under `go test` the package under test compiles as
`-p <import path>`. Check those by building a binary and running it, as
`_integration/otelc-autostart` does.

Before believing a green run, scan for **skipped** tests and check `matched.json` (see Cautions).
`harness.Run` fails loudly on an unwoven build, but the GLS and foundation suites skip themselves.

## Build enough context first

From the sources in `references.md`, learn:
- Orchestrion: the full set of join points and advice, and what advice templates can read (the `.`
  accessors).
- otelc: the seven rule kinds (`inject_hooks`, `inject_code`, `add_struct_fields`, `wrap_call`,
  `add_file`, `assign_value`, `expand_directive`), the `where` and `where.file` selectors, glob and
  `$root` targets, and `version` ranges.

Orchestrion renders Go code templates into the matched AST node. otelc calls external hook functions
through a trampoline and `//go:linkname`, and can also inject raw code in-package (`inject_code`).
Prefer hooks; inject raw code only when it must run inside the target package.

## Workflow

1. List every aspect in `contrib/<name>/orchestrion.yml` as (join point, advice, contrib function it
   calls).
2. Map each aspect with `pattern-mapping.md`, taking exact syntax from `references.md`.
3. Check the whole list from step 1 against `feature-gaps.md`, not just the first aspect that trips
   a gap. Report every gap together, and invent no workarounds.
4. Author `contrib/<name>/otelc/` as **its own Go module**, holding the rules and the before/after
   hooks. Separate module because the hooks import `go.opentelemetry.io/otelc/pkg/hook`, which
   otherwise lands in the `go.mod` of everyone importing the contrib.
   - Module path MUST be `github.com/DataDog/dd-trace-go/contrib/<name>/otelc/v2`, version suffix
     last, like every other module in the repo.
   - Rule files live in this directory, not next to `orchestrion.yml`, named `otelc.yaml`,
     `otelc.yml` or `*.otelc.yaml`. Each rule's `path:` is this package. otelc walks a named
     package's directory tree and stops at nested modules, so `otelc/all` must name this module
     directly.
   - Not under `internal/`: `otelc/all` blank-imports it from outside the contrib's import prefix.
   - State the hooks and the contrib both need is exported from the **contrib package**. The hook
     module cannot reach `contrib/<name>/v2/internal/...`.
   - Keep the hook layer thin. It is the only code that needs otelc to compile; everything else goes
     in normal sub-packages and stays unit-testable.
5. Blank-import `contrib/<name>/otelc` from `otelc/all`, the way `orchestrion/all` lists
   integrations, `go mod tidy` that module, and add the new module to `go.work` (`make fix-modules`
   does not touch the workspace). Applications import `otelc/all`, so nothing else needs editing.
6. Validate both rungs, and diff the otelc spans against the orchestrion ones.
7. From the repository root, in a shell **without** `GOWORK=off` (some generators need the
   workspace, others set `GOWORK=off` for themselves):
   - `make fix-modules`. Replace directives do not propagate from dependency modules, and CI fails
     on an inconsistent module graph.
   - `make lint`, plus `golangci-lint run --disable=gocritic ./...` inside `contrib/<name>` and
     inside `internal/orchestrion/_integration`, which are linted separately.
   - `make generate`, and commit what it changes. A new contrib dependency updates
     `internal/stacktrace/contribs_generated.go`.
8. Keep the PR description short. "Add otelc support for `<name>`" is usually the whole thing. Add a
   note only for an unmigrated aspect, a feature gap, an aspect reproduced by other means with the
   same observable behaviour, or tests added to pin down behaviour the migration relies on.
9. Check CI after opening the PR.
   - Read `Integration Test (ubuntu | stable)` in the `OTelc` workflow first. Matrix jobs nearly
     always fail for the same reason.
   - `failed to load instrumentation packages: ... go: updates to go.mod needed` means a module
     otelc reads has a stale `go.mod`, usually because CI builds a merge commit with a newer `main`.
     Merge `origin/main`, `go mod tidy` the module the error names, and add the new hook module to
     the tidy loop in `.github/workflows/otelc.yml`. Every module otelc reads needs a current
     `go.mod`, including one a rule's `path:` points at, not only modules owning a tool file.
   - Any other failure: fix and push if the cause is obvious, otherwise stop and agree on the fix.

## Cautions

- Reuse the contrib. Functional and performance parity with minimal new code.
- Definition-side double-firing: a hooked constructor that internally calls another hooked
  constructor fires both. Hook only the inner funnel, or add a re-entrancy guard.
- `SetParam` and `SetReturnVal` do not work on a generic target function.
- Upstream limits hook imports to the target library, OpenTelemetry and the standard library, and
  expects hooks to honour `OTEL_GO_ENABLED_INSTRUMENTATIONS` / `OTEL_GO_DISABLED_INSTRUMENTATIONS`.
  Ours import the contrib and follow dd's own configuration instead; say so in the PR.
- Keep comments short: what is not obvious from the code, and nothing else.
- Do not copy rule syntax or API signatures into these docs. Re-read `references.md`.

Two otelc v1.0.1 defects leave a green run with nothing instrumented:

- **Never pass `-json` to `otelc go test`.** It is forwarded into the `go <verb> -a -x -n` dry run
  otelc parses for its build plan, which then arrives empty. Use `-v`, and `go tool test2json -t -p
  <pkg>` afterwards if you need machine-readable output.
- **A package whose only Go files are `_test.go` is skipped.** When it is the package under test the
  whole build loses instrumentation, including rules targeting other modules. Give it a `doc.go`.

Read `.otelc-build/matched.json` to tell instrumented from not: `[]` means matching ran and matched
nothing, `null` means it never got that far (the skipped-package case above).
`.otelc-build/debug/<pkg>/` holds the post-instrumentation source, with slashes in the package path
turned into underscores (`debug/net_http/`). otelc's troubleshooting doc in `references.md` covers
`--debug`, `--stats` and `otelc cleanup`.

## Supporting docs

- `pattern-mapping.md` — Orchestrion aspect patterns to the otelc rule that reproduces them.
- `feature-gaps.md` — Orchestrion constructs otelc cannot reproduce (stop-and-flag cases).
- `references.md` — sources of truth for Orchestrion and otelc.

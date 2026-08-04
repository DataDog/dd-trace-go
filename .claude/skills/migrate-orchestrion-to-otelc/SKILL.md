---
name: migrate-orchestrion-to-otelc
description: >-
  Port a dd-trace-go integration's compile-time auto-instrumentation from Orchestrion to otelc
  (OpenTelemetry Go compile-time instrumentation). Use when an integration already has an
  orchestrion.yml and you need equivalent otelc rules plus hooks that produce the same spans.
---

# Migrate an integration from Orchestrion to otelc

Port one dd-trace-go contrib's Orchestrion auto-instrumentation to otelc. The otelc rules and hooks
must reproduce what the integration's `orchestrion.yml` already does, reusing the existing contrib
entrypoints. Do not reimplement tracing, and do not introduce mechanisms the `orchestrion.yml` does
not use.

**Scope:** work only in the current branch/worktree and the sources in `references.md`. Do not
inspect, borrow from, or depend on other branches, PRs, or worktrees, even if they contain otelc
work. Produce the migration fresh.

## Success criterion

Full parity: the integration's existing Orchestrion integration tests under
`internal/orchestrion/_integration/<name>/` pass built with otelc instead of Orchestrion, with the
same spans. Those tests decide whether the migration is correct.

**Do not edit existing tests or assertions to make the migration pass.** They describe the behaviour
you have to reproduce, so changing them changes the answer. Adding is fine, and often necessary:
new test cases, and stronger assertions on behaviour that was real but never pinned down.

When you find a behaviour difference, write the test before deciding what to do about it:

1. Assert the orchestrion behaviour, as strictly as it deserves. Existing suites often assert that
   the expected spans are present but not that unexpected ones are absent, so "exactly one span"
   usually needs a new assertion rather than an edited one.
2. Run it under orchestrion. It must pass. If it does not, your understanding of the current
   behaviour is wrong, and everything after this is built on that mistake.
3. Run it under otelc and see what actually happens.

Then put the test at the lowest level that can still fail. Most of what a migration changes is
ordinary contrib behaviour, so a unit test in `contrib/<name>` with `mocktracer` is usually enough
and runs in seconds. Reach for `_integration` only when the assertion genuinely needs a woven build.
A mechanism the hooks depend on is often observable directly: `TestDialMarksItsOwnDial` in
`contrib/gomodule/redigo` pins the contract the redigo hook relies on with no server and no otelc.

Check the test fails when you break the thing it guards. Several of these guards passed for the
wrong reason on the first attempt.

Reasoning from the call graph is not a substitute for step 3. A difference you argued for on paper
and never ran is a guess, and it should be labelled as one until a test says otherwise.

Two rungs, in order, both run from `internal/orchestrion/_integration`:
- **Compile + inject.** `otelc go build ./<name>/...`. If it compiles with the hooks injected, the
  `otelc.yaml` and hook package are structurally sound.
- **Span parity.** `otelc go test ./<name>/...`, then the same suite under `orchestrion go test`, and
  compare. The foundation this needs (tracer lifecycle, GLS bridge, harness support) is already in
  the repo, so do not build it and do not go looking for it in other branches, PRs, or worktrees.

A green run is not enough on its own: check the output for tests that **skipped**. Most of these
suites skip themselves when instrumentation is missing, so a build where nothing was woven reports
as passing. See the two failure modes under Cautions.

## Build enough context first

Before touching an integration, understand both sides. Read `references.md` and, from the sources it
links, learn:
- Orchestrion: what a join point and an advice are, the full set available, and what the advice
  templates can read (the `.` accessors).
- otelc: the rule kinds, their exact syntax, and when to use each.

Two facts to keep straight:
- These are dd's own otelc rules for dd-trace-go. The hooks import dd-trace-go and call the existing
  contrib, the same way the `orchestrion.yml` aspects do today.
- Orchestrion renders Go code templates into the matched AST node. otelc's normal path is external Go
  hook functions linked in via a trampoline plus `//go:linkname`; it can *also* inject raw code into
  a body (`inject_code`), like Orchestrion. Prefer hooks; use raw in-body injection only when the
  code must run inside the target package.

## Workflow

1. Enumerate the source: read `contrib/<name>/orchestrion.yml`; list each aspect as (join point,
   advice, which contrib function it calls).
2. Map each aspect to the otelc rule that reproduces it, using `pattern-mapping.md`. Take exact rule
   syntax from the sources in `references.md`, not from memory.
3. Check `feature-gaps.md`. If an aspect needs something otelc has no equivalent for, stop and flag
   it rather than inventing a workaround.
4. Author `contrib/<name>/otelc/` as **its own Go module**, holding everything specific to otelc: the
   rules (`otelc.yaml`) and the before/after hooks, plus any helper code only they use. The rules'
   `path:` points at this same package.
   - Its own module because the hooks import `go.opentelemetry.io/otelc/pkg/hook`, and a package
     inside the contrib module would put that dependency in the `go.mod` of everyone importing the
     contrib, otelc user or not. Module path `.../contrib/<name>/v2/otelc` (no trailing version
     element) keeps it under the contrib's import prefix, so it can still import the contrib's
     `internal/` packages.
   - The rules go **in this directory, not next to `orchestrion.yml`**. otelc loads rule files from
     the directory of the package the tool file imports, so rules and hooks have to be one package
     for a single import to pull in both.
   - Do not put the hook package under `internal/`: it is blank-imported into the built app's
     module, which cannot import `contrib/<name>/internal/...`.
   The hooks call the existing contrib entrypoints; keep injection-independent logic in normal
   sub-packages and let only the thin hook layer touch injected fields.

   State the hooks and the contrib both need goes in the **contrib package itself**, exported, not
   in an `internal/` helper. The hook module can reach `internal/`, but a caller-visible name says
   what it is for (see `redigotrace.TraceMark`).
5. Enable the integration by blank-importing `contrib/<name>/otelc` from `otelc/all`, the way
   `orchestrion/all` lists integrations, then `go mod tidy` that module. Applications import
   `otelc/all`, so nothing else needs editing.
6. Validate both rungs from the Success criterion, and diff the otelc spans against the orchestrion
   ones.
7. Before opening the PR, from the repository root:
   - `make fix-modules`. Adding a module or a dependency leaves `go.mod`/`go.sum` and the replace
     directives inconsistent, and CI fails on it. Every module in the graph needs its own replace
     in the **main** module being built, because replaces in dependency modules are ignored.
   - `make lint`, and fix what it reports. Submodules are linted separately, so also run
     `golangci-lint run --disable=gocritic ./...` inside `contrib/<name>` and inside
     `internal/orchestrion/_integration`.
   - `make generate`, and commit whatever it changes. Adding a dependency to a contrib module
     updates `internal/stacktrace/contribs_generated.go`, and CI fails when generated files are
     stale.

   Run these in a shell **without** `GOWORK=off`. Some generators need the workspace to resolve the
   contrib modules and crash without it, while others set `GOWORK=off` for themselves. Exporting it
   globally for otelc work breaks the first group.
8. Keep the PR description short. "Add otelc support for `<name>`" is usually the whole thing. Add a
   note only for what a reviewer would otherwise have to find on their own: an aspect left
   unmigrated, a feature gap, an aspect reproduced by different means but with the same observable
   behaviour, or tests added to the orchestrion or contrib side to pin down behaviour the migration
   relies on.
9. After opening the PR, check its CI. Pushing is not the end of the task.
   - Read the `OTelc` workflow first. When every matrix job fails they nearly always fail for the
     same reason, so read only `Integration Test (ubuntu | stable)` and skip the rest.
   - `failed to load instrumentation packages: ... go: updates to go.mod needed` means a module
     otelc reads has a stale `go.mod`. GitHub runs CI on a candidate merge commit, so a dependency
     bump that landed on `main` after the branch was cut raises the build list of every module that
     replaces `dd-trace-go/v2` with a local path. Auto-pin recurses into each module that owns a
     tool file (`otel.instrumentation.go` / `otelc.tool.go`) and runs `go list` from that module's
     directory, where read-only mode turns the stale `go.mod` into a hard error. Fix: merge
     `origin/main` and `go mod tidy` the module the error names. If the migration gave a new module
     its own tool file, add that module to the tidy loop in `.github/workflows/otelc.yml`, which
     exists for exactly this and only covers the modules listed in it.
   - Any other failure: fix and push if the cause is obvious. Otherwise stop, explain the failure,
     and agree on the fix before changing anything.

## Cautions

- Reuse the contrib. The goal is functional and performance parity with minimal new code.
- A hook file that references an injected field only compiles under otelc (not a plain `go build`),
  so keep it thin. Everything else about the migration should still be unit-testable.
- Keep comments short. Say what is not obvious from the code and stop; do not restate the diff or
  explain why something is absent.
- Definition-side double-firing: otelc hooks a definition, so a constructor that internally calls
  another hooked constructor fires both. Hook only the inner funnel, or add a re-entrancy guard.
- Do not copy rule syntax or API signatures into these docs; they drift. Re-read `references.md`.

Two otelc defects that produce a passing run with nothing instrumented. Both cost a CI cycle to
find, so check for them before believing a green result (otelc v1.0.1):

- **Never pass `-json` to `otelc go test`.** otelc derives its build plan by parsing the text output
  of `go test -a -x -n`, your `-json` is forwarded into that command, and the plan comes back
  unreadable. It then instruments nothing, with no error. Use `-v` and convert afterwards with
  `go tool test2json -t -p <pkg>` if you need machine-readable output.
- **A package with only `_test.go` files gets skipped**, after which otelc finds no tool file, falls
  back to its embedded default rules, and instruments nothing. Give such a package a `doc.go`.

To tell instrumented from not, check `.otelc-build/matched.json`: `null` means no rule matched.
`.otelc-build/debug/<pkg>/` holds the post-instrumentation source, which is the fastest way to see
whether a rule did what you meant.

## Supporting docs

- `pattern-mapping.md` — Orchestrion aspect patterns to the otelc rule that reproduces them.
- `feature-gaps.md` — Orchestrion constructs otelc cannot reproduce (stop-and-flag cases).
- `references.md` — sources of truth for Orchestrion and otelc.

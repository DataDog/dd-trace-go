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

**Scope:** work only in the current branch/worktree and the sources in `references.md`. The
foundation those sources describe is on `main`, so branch from `main` and everything you need is
present. Do not inspect, borrow from, or depend on other branches, PRs, or worktrees, even if they
contain otelc work.

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

Before believing a red one, check who owns the missing spans. A suite often asserts spans produced
by another contrib, so it cannot reach parity until that one is migrated too; grep the expected
trace for `"component"` values that are not yours. That is a sequencing problem, not a gap, and the
integration is not blocked. Say so, and do not open a PR until the suite is actually green.

## Build enough context first

From the sources in `references.md`, learn:
- Orchestrion: the full set of join points and advice, and what advice templates can read (the `.`
  accessors).
- otelc: the eight rule kinds (`inject_hooks`, `inject_code`, `add_struct_fields`, `wrap_call`,
  `add_file`, `assign_value`, `expand_directive`, `set_fields`), the `where` and `where.file`
  selectors, glob and `$root` targets, and `version` ranges.

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
   otherwise lands in the `go.mod` of everyone importing the contrib. A hook package sharing a
   module with the code it instruments also breaks `otelc go test ./...`: the generated
   `otelc.runtime.go` self-imports, and once two packages in the module carry that file the link
   fails on a duplicated `OtelGetStackImpl` symbol.
   - Module path MUST be `github.com/DataDog/dd-trace-go/contrib/<name>/otelc/v2`, version suffix
     last, like every other module in the repo.
   - The package needs at least one Go file, and that file must import the contrib the rules name
     in their `imports:` map. Two separate failures, so a rules-only module needs both:
     - No Go file at all and otelc stops with `package <path> is not part of a module`. A bare
       package clause is enough to clear this; the file name does not matter (`doc.go`, `otelc.go`).
     - A Go file that never mentions the contrib leaves nothing in the module's own source
       requiring it, so `go mod tidy` drops the requirement while `make fix-modules` keeps the
       replace directive, and otelc's toolexec fails with "replaced but not required". Blank-import
       it (`_ "github.com/DataDog/dd-trace-go/contrib/<name>/v2"`) to hold it in place.

     A module with hooks gets the second for free, since the hooks import the contrib to call it.
   - Rule files live in this directory, not next to `orchestrion.yml`, named `otelc.yaml`,
     `otelc.yml` or `*.otelc.yaml`. Each rule's `path:` is this package. otelc walks a named
     package's directory tree and stops at nested modules, so `otelc/all` must name this module
     directly.
   - Do not add the file-level `version:` key yet. It only exists on otelc main, and a file
     carrying it fails to parse on every released version, so there is nothing valid to declare
     while `OTELC_VERSION` is a main commit. otelc warns about its absence into
     `.otelc-build/debug.log` only.
   - Not under `internal/`. otelc blank-imports the packages it reads rules from into the
     application's own main package, so a rule-carrying package under `internal/` builds here and
     fails for every real user. `scripts/build_otelc_external_app.sh` guards this; a rule's
     `target:` may still be internal, since targets are rewritten rather than imported.
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
   - `make fix-modules`, repeatedly until it stops changing files. It snapshots the module graph
     once at start, so a brand new module needs two or three passes; the first often fails with
     `unknown revision 000000000000`. Replace directives do not propagate from dependency modules,
     and CI fails on an inconsistent module graph.
   - `make lint`. It reaches the root module and `internal/orchestrion/_integration` (the latter
     with `--disable=gocritic`) and nothing else, and CI lints the same two. Lint the contrib
     modules yourself: `golangci-lint run ./...` inside `contrib/<name>`, and again inside
     `contrib/<name>/otelc`.
   - `make generate`, and commit what it changes. A new contrib dependency updates
     `internal/stacktrace/contribs_generated.go`. Run `otelc cleanup` first: a stale `.otelc-build`
     makes it fail with "expected exactly 1 package, got 0".
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
- On a generic target function, otelc replaces `GetParam`, `SetParam`, `GetReturnVal` and
  `SetReturnVal` with panicking stubs, so a hook that only reads an argument fails too. Reach the
  values another way, or use `inject_code`, which has no such limit.
- `SetReturnVal` also panics in a before-only hook, because `returnVals` is allocated only for an
  after trampoline. Pair before and after hooks and pass the value through `SetData`/`GetData`.
- A hook that panics is swallowed by otelc's generated `recover()`: green build, green tests, no
  instrumentation. It prints `failed to exec Before hook <name>` to stderr and nothing more, so
  assume this whenever spans go missing without an error.
- `inject_hooks` gives no init-ordering guarantee. The trampoline links through
  `//go:linkname` and creates no import edge, so hooking a function that other packages call from
  their own `init()` can run the hook before the hook module's own `init()`.
- `target: $root` never matches `package main`, whose compile-time import path is the literal
  string `main`. A call-site rule that must fire there needs a `$root` rule and a `main` rule.
- Hook modules must pin `go.opentelemetry.io/otelc/pkg` to the same commit as `OTELC_VERSION`, not
  to whatever is current. There is no submodule tag, so plain `go mod tidy` drifts them onto HEAD.
  Both pseudo-versions carry that commit, so read the hash out of `OTELC_VERSION` and resolve it:
  `go list -m go.opentelemetry.io/otelc/pkg@<hash>`. After changing it, tidy the hook module and
  `otelc/all`, or the next build fails with "updates to go.mod needed" for a module otelc reads.
- Upstream limits hook imports to the target library, OpenTelemetry and the standard library, and
  expects hooks to honour `OTEL_GO_ENABLED_INSTRUMENTATIONS` / `OTEL_GO_DISABLED_INSTRUMENTATIONS`.
  Ours import the contrib and follow dd's own configuration instead; say so in the PR.
- Keep comments short: what is not obvious from the code, and nothing else.
- Do not copy rule syntax or API signatures into these docs. Re-read `references.md`.

One otelc defect still leaves a green run with nothing instrumented:

- **A package whose only Go files are `_test.go` is skipped.** otelc logs `skipping package without
  Go files`, silently falls back to its embedded default rules, and writes `matched.json` as `null`.
  When it is the package under test the whole build loses instrumentation, including rules targeting
  other modules. Give it a `doc.go`.

`otelc go test -json` used to fail the same way, forwarding `-json` into the `go <verb> -a -x -n`
dry run otelc parses for its build plan and leaving `matched.json` as `[]` on an otherwise green
run. Fixed in open-telemetry/opentelemetry-go-compile-instrumentation#1345, which the pinned version
includes, so `-json` is safe. `.github/workflows/otelc.yml` still converts `-v` output with `go tool
test2json` from when it was not.

Read `.otelc-build/matched.json` to tell instrumented from not: `[]` means matching ran and matched
nothing, `null` means it never got that far (the skipped-package case above).
`.otelc-build/debug/<pkg>/` holds the post-instrumentation source, with every slash and dot in the
package path turned into an underscore (`net/http` becomes `debug/net_http/`,
`example.com/app` becomes `debug/example_com_app/`). otelc's troubleshooting doc in `references.md`
covers `--debug`, `--stats` and `otelc cleanup`.

`.otelc-build/` is already gitignored here. Every build writes one next to the module it ran in, so
if you validate from outside this repo, ignore it there before committing anything.

## Supporting docs

- `pattern-mapping.md` — Orchestrion aspect patterns to the otelc rule that reproduces them.
- `feature-gaps.md` — Orchestrion constructs otelc cannot reproduce (stop-and-flag cases).
- `references.md` — sources of truth for Orchestrion and otelc.

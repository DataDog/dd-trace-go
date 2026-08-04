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
same spans. Those tests decide whether the migration is correct. Do not weaken them.

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
4. Author, alongside the contrib:
   - `contrib/<name>/otelc.yaml` — the otelc rules, placed next to the existing `orchestrion.yml`.
   - `contrib/<name>/otelc/` — a package holding everything specific to otelc: the before/after
     hooks and any helper code only the rules or hooks use. This is not useful to regular contrib
     users, so keep it out of the main contrib package. The rules' `path:` points at this package.
     Do not put it under `internal/`: otelc blank-imports the hook package into the built app's
     module, which cannot import a package under `contrib/<name>/internal/`.
   The hooks call the existing contrib entrypoints; keep injection-independent logic in normal
   sub-packages and let only the thin hook layer touch injected fields.
5. Enable the integration via otelc's import-driven tool file (see `references.md`).
6. Validate both rungs from the Success criterion, and diff the otelc spans against the orchestrion
   ones.

## Cautions

- Reuse the contrib. The goal is functional and performance parity with minimal new code.
- A hook file that references an injected field only compiles under otelc (not a plain `go build`),
  so keep it thin and cover it with integration tests, not standalone unit tests.
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

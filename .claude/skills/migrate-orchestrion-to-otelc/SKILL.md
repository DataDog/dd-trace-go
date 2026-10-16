---
name: migrate-orchestrion-to-otelc
description: >-
  Port a dd-trace-go integration's compile-time auto-instrumentation from Orchestrion to otelc
  (OpenTelemetry Go compile-time instrumentation). Use when an integration already has an
  orchestrion.yml and you need equivalent otelc rules plus hooks that produce the same spans.
---

# Migrate an integration from Orchestrion to otelc

Reproduce the behaviour of the integration's `orchestrion.yml` with otelc rules and hooks that call
the existing contrib entrypoints (`WrapClient`, `Middleware`, `Open`, ...). Do not reimplement
tracing. You do not have to mirror each aspect one to one: when otelc has its own way to get the
same behaviour, prefer it.

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

Instrumentation can only be tested in an otelc or orchestrion build, so tests for it go in
`_integration/<name>/`. A `contrib/<name>` unit test with `mocktracer` only covers contrib code the
port adds or changes, such as state exported for the hooks. Check that each test fails when you
break what it guards.

## Validation

Run from `internal/orchestrion/_integration`:

1. `otelc go test ./<name>/...`, and the same suite with `orchestrion go test`. Test one package per
   invocation, as CI does: naming two packages where one imports the other fails the link with
   `duplicated definition of symbol ... OtelGetStackImpl`, an open otelc bug
   (https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation/issues/1361).
2. Check what otelc did. A passing suite does not prove the rules are right: it matches spans by
   subset, so it misses extra spans and double wrapping.
   - `.otelc-build/matched.json` lists every rule that matched, by package and file. Each of your
     rules should appear against the packages you expected, and nowhere else. `[]` means matching
     ran and matched nothing, `null` means it never got that far.
   - `.otelc-build/debug/<pkg>/` holds every file otelc rewrote, with slashes and dots in the
     package path turned into underscores (`net/http` becomes `debug/net_http/`). Read each
     injection and check it is the code you meant.

Before believing a green run, scan for **skipped** tests and check for a silent failure (see
"Green runs with nothing instrumented"). `harness.Run` fails loudly on an unwoven build, but the GLS
and foundation suites skip themselves.

Before believing a red one, check who owns the missing spans. A suite often asserts spans produced
by another contrib; grep the expected trace for `"component"` values that are not yours. That is a
sequencing problem, not a gap, and the integration is not blocked. Say so, and do not open a PR
until the suite is green.

### Package main and dependency modules

The harness suites never build a `package main`, and only exercise call sites inside the
`_integration` module. Add a test for each case your rules reach:

- **Package main**, when a `target:` includes `main` or `**`. Under `go test` the package under
  test compiles as `-p <import path>`, so these rules never fire there. Add
  `_integration/<name>/mainapp/`, a `package main` that calls the library and returns normally so
  the injected `defer tracer.Stop()` flushes. Add a `main_test.go` that runs it with
  `maintest.Run(t, "./<name>/mainapp")` and asserts spans with `agent.RequireSpan`
  (`chi/main_test.go`). For a call-site rule, also call the library under an import alias
  (`chi/mainapp/alias.go`).
- **Dependency modules**, when a call-site rule targets `**`. Such a rule exists to also rewrite
  call sites inside third-party dependencies, as orchestrion does. Every call site in the suites
  belongs to `_integration`, the module being built, so a rule that never reaches a dependency
  passes them anyway. Add a small module under `_integration/internal/<lib>dep/` (module path
  `example.com/<lib>dep`) that calls the library, `require` and `replace` it in
  `_integration/go.mod`, and add a `TestCaseDependency` that calls it (`chi/dependency.go`). It has
  to be a separate module, not a package of `_integration`, to count as a dependency.

`maintest` builds the app with otelc and runs it against `maintest.Agent`, the harness test agent
served on a local port so that another process can report to it. It skips when `otelc` is not on
`PATH`. To compare against a plain `go build`, or to count the requests that reach the agent, use
`maintest.Build`, `NewAgent` and `Exec` directly (`otelc-autostart/autostart_test.go`).

## Build enough context first

From the sources in `references.md`, learn:
- Orchestrion: the full set of join points and advice, and what advice templates can read (the `.`
  accessors).
- otelc: every rule kind, the `where` and `where.file` selectors, the target forms and `version`
  ranges, from `docs/rules.md`.

Orchestrion renders Go code templates into the matched AST node. otelc calls external hook functions
through a trampoline and `//go:linkname`, and can also inject raw code in-package (`inject_code`).
Prefer hooks; inject raw code only when it must run inside the target package.

## Workflow

1. List every aspect in `contrib/<name>/orchestrion.yml` as (join point, advice, contrib function it
   calls).
2. Map each aspect with `pattern-mapping.md`, taking exact syntax from `references.md`.
3. Check the whole list against `feature-gaps.md`, not just the first aspect that trips a gap.
   Report every gap together instead of hacking around it.
4. Author `contrib/<name>/otelc/` as **its own Go module**, holding the rules and the hooks. The
   hooks import `go.opentelemetry.io/otelc/pkg/hook`, which would otherwise land in the `go.mod` of
   everyone importing the contrib.
   - Module path MUST be `github.com/DataDog/dd-trace-go/contrib/<name>/otelc/v2`, version suffix
     last, like every other module in the repo.
   - The package needs at least one Go file, and that file must import the contrib. With no Go file,
     otelc stops with `package <path> is not part of a module`. Without the import, `go mod tidy`
     drops the requirement while `make fix-modules` keeps the replace directive, and otelc fails
     with "replaced but not required". A rules-only module blank-imports the contrib
     (`_ "github.com/DataDog/dd-trace-go/contrib/<name>/v2"`); a module with hooks already imports
     it.
   - Rule files live in this directory, named `otelc.yaml` or `*.otelc.yaml` (`.yml` works too).
     Each rule's `path:` is this package.
   - Do not add the file-level `version:` key. It rejects pseudo-versions, and `OTELC_VERSION` is
     a `main` commit, so there is nothing valid to declare.
   - Not under `internal/`. otelc blank-imports the packages it reads rules from into the
     application's main package, so a rule-carrying package under `internal/` fails for every real
     user. `scripts/build_otelc_external_app.sh` guards this. A rule's `target:` may still be
     internal.
   - State the hooks and the contrib both need is exported from the **contrib package**. The hook
     module cannot reach `contrib/<name>/v2/internal/...`.
   - Keep the hook layer thin. It is the only code that needs otelc to compile; everything else goes
     in normal sub-packages and stays unit-testable.
5. Wire the module in:
   - Blank-import it from `otelc/all`, the way `orchestrion/all` lists integrations, and
     `go mod tidy` that module. otelc stops at nested modules, so `otelc/all` must name this module
     directly. Applications import `otelc/all`, so nothing else needs editing.
   - Add it to `go.work` (`make fix-modules` does not touch the workspace).
   - Add the contrib and its `otelc` module to `OTELC_MODULES` in `.github/workflows/otelc.yml`,
     which CI tidies before running otelc. Every module otelc reads needs a current `go.mod`.
6. Run the validation steps, plus the main app and dependency tests your rules need, and diff the
   otelc spans against the orchestrion ones.
7. From the repository root, in a shell **without** `GOWORK=off`:
   - `make fix-modules`, repeatedly until it stops changing files. A brand new module needs two or
     three passes; the first often fails with `unknown revision 000000000000`.
   - `make lint`, which covers only the root module and `internal/orchestrion/_integration`. Also
     run `golangci-lint run ./...` inside `contrib/<name>` and `contrib/<name>/otelc`.
   - `otelc cleanup`, then `make generate`, and commit what it changes (a new contrib dependency
     updates `internal/stacktrace/contribs_generated.go`). A stale `.otelc-build` makes it fail with
     "expected exactly 1 package, got 0".
8. Keep the PR description short. "Add otelc support for `<name>`" is usually the whole thing. Add a
   note only for an unmigrated aspect, a feature gap, an aspect reproduced by other means with the
   same observable behaviour, or tests added to pin down behaviour the migration relies on.
9. Check CI after opening the PR.
   - Read `Integration Test (ubuntu | stable)` in the `OTelc` workflow first. Matrix jobs nearly
     always fail for the same reason.
   - `failed to load instrumentation packages: ... go: updates to go.mod needed` means a module
     otelc reads has a stale `go.mod`, usually because CI builds a merge commit with a newer `main`.
     Merge `origin/main` and `go mod tidy` the module the error names.
   - Any other failure: fix and push if the cause is obvious, otherwise stop and agree on the fix.

## Cautions

### Hooks

- Upstream limits hook imports to the target library, OpenTelemetry and the standard library, and
  expects hooks to honour `OTEL_GO_ENABLED_INSTRUMENTATIONS` / `OTEL_GO_DISABLED_INSTRUMENTATIONS`.
  Ours import the contrib and follow dd's own configuration instead; say so in the PR.
- A hooked constructor that calls another hooked constructor fires both. Hook only the inner
  funnel, or add a re-entrancy guard.
- Orchestrion weaves an aspect into dd-trace-go's own packages, contribs included, only when the
  aspect sets `tracer-internal: true`, so its call-site aspects skip the contrib's own calls. otelc
  has no such rule, and a definition-side hook fires for every caller. A hook on a library
  constructor fires again when the contrib's wrapper calls it, so a client built with the contrib
  is traced twice. A `wrap_call` rule with the contrib excluded from `target:` avoids this, as
  orchestrion's call-site aspects do. For a definition-side hook, prefer a runtime guard: the
  contrib marks a value the hook can read, such as the context
  (`contrib/gomodule/redigo/otelc/hook.go`). It skips only this hook and follows the context into
  new goroutines. When there is no such value, put `//otelc:ignore` above the contrib's call. It
  skips every hook on that goroutine for the whole statement, other integrations' too, and does
  not reach goroutines the call starts.
- Give every `inject_hooks` rule its own before/after names. otelc emits a hook's `//go:linkname`
  stub once per rule and file, so two rules sharing a hook in one package fail with `redeclared in
  this block`. Delegate to a shared helper instead.
- Do not declare `...any` for a variadic parameter. otelc keeps the target's element type in the
  stub, so the hook reads a garbage slice with a green build. If the element is a func type the
  hook cannot name, declare `_ ...func()` and never read it (every func value is one pointer).
- On a generic target function, `GetParam`, `SetParam`, `GetReturnVal` and `SetReturnVal` are
  panicking stubs, so even a hook that only reads an argument fails. Use `inject_code` instead
  (https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation/issues/1280).
- `SetReturnVal` panics in a before-only hook, because only the after trampoline allocates
  `returnVals`. Pair before and after hooks and pass the value through `SetData`/`GetData`.
- `inject_hooks` gives no init-ordering guarantee. The trampoline creates no import edge, so a
  function that other packages call from their own `init()` can run the hook before the hook
  module and the contrib are initialized. Queue those calls in package variables with no
  initializer and replay them from the hook module's `init()`
  (`contrib/database/sql/otelc/hooks.go`).
- Every hook call allocates: the trampoline builds a `HookContext` holding pointers to the
  parameters, and it escapes. On a hot path in our own code, prefer `inject_code`.

### Rules

- A call-site rule that must also reach call sites in dependencies targets `**` and excludes the
  library and its contrib with `not:` entries (`contrib/go-chi/chi/otelc/otelc.yaml`).
- `target: $root` never matches `package main`, whose compile-time import path is the literal
  string `main`. Only a `main` entry reaches it, so a rule for the root module and its main package
  lists both (`target: [$root, main]`). It also needs a main app test (see "Package main and
  dependency modules").

### The otelc pin

- Hook modules pin `go.opentelemetry.io/otelc/pkg` to the commit in `OTELC_VERSION`. There is no
  submodule tag
  (https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation/issues/984), so
  plain `go mod tidy` drifts them onto HEAD. Resolve the hash from
  `OTELC_VERSION` with `go list -m go.opentelemetry.io/otelc/pkg@<hash>`, then tidy the hook module
  and `otelc/all`, or the next build fails with "updates to go.mod needed".

### Green runs with nothing instrumented

- A hook that panics is swallowed by otelc's generated `recover()`. Build and tests stay green, and
  the only sign is a `failed to exec Before hook <name>` line on stderr. Suspect this whenever
  spans go missing without an error.
- A package whose only Go files are `_test.go` is skipped. otelc logs `skipping package without Go
  files` and falls back to its embedded default rules, so the whole build loses instrumentation
  and `matched.json` is `null`. Give it a `doc.go`.
- `.otelc-build/` is gitignored here. If you validate from outside this repo, ignore it there too.
  otelc's troubleshooting doc covers `--debug`, `--stats` and `otelc cleanup`.

## Supporting docs

Do not copy rule syntax or API signatures into these docs; link the source in `references.md`.

- `pattern-mapping.md`: Orchestrion aspect patterns to the otelc rule that reproduces them.
- `feature-gaps.md`: Orchestrion constructs otelc cannot reproduce (stop-and-flag cases).
- `references.md`: sources of truth for Orchestrion and otelc.

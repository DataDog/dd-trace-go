# AGENTS.md

Guidance for coding agents in this repository. `CLAUDE.md` is just `@AGENTS.md`,
so this applies to Claude Code, Codex, Cursor and anything else reading
`AGENTS.md`. Follow [Effective Go](https://go.dev/doc/effective_go).

This file covers only what the code cannot tell you: what CI rejects, and what
reviewers reliably send back. Directory layout, existing patterns and how an
integration is wired — read the neighbouring source.

## What reviewers send back

Distilled from human review comments on the last 100 merged PRs. These are the
recurring ones, most frequent first.

**Tests must pin behaviour, not shape.** Assert the actual value — the specific
frame, the exact tag, the concrete output. `len(x) == 1`, `assert.NotNil`, and
"it didn't panic" all pass for degenerate results. Reviewers also reject tests
that reach through `reflect` or call unexported methods to poke at
implementation details: drive the public entry point and assert what comes out.
A new `With*` option needs a test proving its effect on the config. Put new
cases in the existing `_test.go` for that package rather than a new file.

**Comments must survive the pull request.** Anything referencing "the previous
behaviour", "what we're replacing", or the PR discussion is dead on arrival —
the reader six months out has none of that. When you change what a function
does, update its doc comment in the same commit; stale comments that contradict
the code get flagged every time. One doc comment per exported field, not one
covering several. A `var _ Iface = (*T)(nil)` assertion needs no comment.
Delete scaffolding an agent leaves behind — enumerated "Approach A / Approach
B" notes, restatements of the diff, and comments explaining obvious code are
called out explicitly in review.

**Do not add public surface you cannot justify.** The most common single word of
criticism in this repo's reviews is "overkill". Before exporting anything: does
it have a non-test caller? Does an existing grouped accessor already own this
capability? A required parameter belongs in the signature, not in a functional
option. A function taking exactly one item should not be variadic. If you
touched an exported symbol under `ddtrace/tracer`, run `make apidiff`.

**Hot-path changes need numbers.** A benchmark bot comments on every PR and
allocation regressions block merge. If you add work to span finish, propagation,
or encoding, benchmark it and put the result in the description — and check the
benchmark actually reaches your new code, since one that never enters the new
branch measures nothing.

**Errors: `fmt.Errorf` unless a caller must inspect it.** Custom error types are
for values callers match with `errors.Is`/`errors.As`. Errors that only reach a
log do not need a type.

**Guard the degenerate case.** Early-return on nil, zero-value and empty input.
A failed or malformed write must never destroy data already accumulated — keep
the previous value and log. Anything ingested from outside (headers, tracestate,
response bodies) needs an explicit size cap.

**Write the PR description as problem → solution → verification.** With
evidence for the problem and commands for the verification. Reviewers have
asked for tighter descriptions and skipped the sprawling
"alternatives considered" sections.

## Traps that CI rejects

Correct-looking code that compiles and passes `go test`.

**Dependency changes.** Every `contrib` module has its own `go.mod`, and
`go.work.sum` must stay in step:

```shell
go get <import-path>@<version>
make fix-modules   # required
make generate
```

Prefer the **minimum secure version**, not the latest — downstream users inherit
the bump. Walking modules by hand with `go get` + `go mod tidy` leaves
`go.work.sum` and indirect-only modules stale; `go work sync` overcorrects and
rewrites unrelated dependencies. Neither is a substitute for `make fix-modules`.

**Environment variables.** `os.Getenv` and `os.LookupEnv` are forbidden — use
`env.Get` / `env.Lookup` from `internal/env`, or `instrumentation/env` from a
`contrib` module, which cannot import `internal/`. Only `DD_`- and
`OTEL_`-prefixed names are validated against the allowlist. To add one:

```shell
go run ./scripts/configinverter/main.go add DD_MY_NEW_KEY
go run ./scripts/configinverter/main.go generate
go run ./scripts/configinverter/main.go check
```

`add` writes `FIX_ME` placeholders into
`internal/env/supported_configurations.json` — replace them by hand: `type` is
one of `string`, `boolean`, `int`, `decimal`, `array`, `map`; `default` is a
string or `null`; `implementation` is `"A"`. Never write that file or
`supported_configurations.gen.go` any other way — re-serialising drops the
top-level `"version"` field and reorders every entry. A Datadog maintainer must
also register the key internally or the GitLab
`validate_supported_configurations_local_file` job fails; say so in the PR.

**Locks.** `sync.Mutex`/`sync.RWMutex` are forbidden **only under
`ddtrace/tracer/`**, where `internal/locking` replaces them; elsewhere plain
`sync` is right. Preserve every `// +checklocks:` annotation exactly.

**Goroutine leaks.** Core packages assert on `uber-go/goleak`. A leak that only
reproduces in CI is usually an idle HTTP connection to a local Datadog Agent.

## Tests and verification

`make test/unit` is the **root module only** — it does not compile `contrib`. A
contrib change is untested until you run it in its own module:

```shell
cd contrib/google.golang.org/grpc && go test -race -run TestName ./...
```

```shell
go test -race -run TestName ./ddtrace/tracer/   # root module, single test
make test/contrib                               # every contrib module
INTEGRATION=1 go test -race ./...               # needs Docker
make format && make lint                        # pinned tools, reproduces CI
```

Fixing a flake? Prove it with `-count=100`.

## Read before working in these areas

| File | Covers |
|---|---|
| [contrib/AGENTS.md](./contrib/AGENTS.md) | Integrations — naming, testing, adding one |
| [ddtrace/tracer/AGENTS.md](./ddtrace/tracer/AGENTS.md) | Core tracer and API changes |
| [internal/AGENTS.md](./internal/AGENTS.md) | Non-customer-facing packages |
| [orchestrion/AGENTS.md](./orchestrion/AGENTS.md) | Compile-time auto-instrumentation |
| [profiler/AGENTS.md](./profiler/AGENTS.md) | Profiler |

[CONTRIBUTING.md](./CONTRIBUTING.md) has the long form of all of the above.

## Keeping this file current

Add a row above when a new `AGENTS.md` appears. Keep this file to what the code
cannot tell you — density over length.

Update [CONTRIBUTING.md](./CONTRIBUTING.md) when a change adds a new way to
interact with or configure the tracer, an internal replacement for a standard
library package, a new `make` target, or a new CI workflow. Update
[README.md](./README.md) for new `make` options and commands essential to
testing or building. If a change warrants one and the author has not made it,
say so.

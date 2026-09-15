# Error Tracking Audit

`errtrackaudit` reports the `internal/log.Error`/`Warn` call sites in the root
module that may want to adopt the Error Tracking reporting API
(`internal/telemetry/log.ReportError` / `ReportPanic` /
`LogAndReportError` / `LogAndReportPanic`), grouped by the `CODEOWNERS` team
that owns each file. It uses the same dependency-free `internal/codeowners`
parser and matcher as CI Visibility. Repository-specific CODEOWNERS pattern
validation remains in `scripts/check_codeowners.go`. The audit is the triage
tool for the adoption policy in [internal/README.md](../../internal/README.md#telemetry)
("When to report, and when not to").

## Scope

The audit covers the root module only. The scan loads a single Go module
(`packages.Load` with `GOWORK=off` does not cross module boundaries), so the
`contrib/*` integration modules and the workspace-sibling tooling modules are
out of scope by construction — their module paths sit outside
`github.com/DataDog/dd-trace-go/v2/`, so they cannot import the
`internal/telemetry/log` package at all. Two directories inside the root module
are excluded because their call sites can never adopt the API:

* `internal/log/` — the logger's own implementation. `internal/telemetry/log`
  imports `internal/log`, so the reverse edge is a compile-time import cycle.
* `internal/telemetry/log/` — the reporting API's own implementation:
  `LogAndReportError` and friends call `internal/log.Error` on the caller's
  behalf.

Call sites are identified through type information, not text: an `Error` or
`Warn` call is audited when its callee resolves to the `internal/log` package
through the type checker. Arbitrary import aliases (`logger.Error`,
`internallog.Error`, …) and dot imports are therefore identified reliably,
while same-named methods, struct fields, and functions in other packages are
not. Calls are visited everywhere they can appear, including package-level
variable initializers (`ddtrace/tracer/time_windows.go`'s `var now = func()`
initializer is the canonical in-repo example), which are reported with
`(package-init)` as their enclosing function.

The scan loads both `linux/amd64` and `windows/amd64` with cgo disabled, then
merges the results by source location. This keeps CI's inventory stable and
includes the repository's Windows-only log sites. Add another build platform
to `defaultScanOptions` if a future audited call is guarded by a different
platform constraint. A package that fails to load or type-check on either
platform fails the whole audit rather than silently reporting a partial
inventory.

## Classification

| Label | Meaning |
|---|---|
| `CANDIDATE` | The constant message shows no strongly user-facing signal. A human applies the 4-point policy. |
| `LIKELY_INELIGIBLE` | The constant message mentions an environment variable, a rejected user-supplied value (`invalid`, `unsupported`, …), a missing/overridden setting, or skipped user input — per the policy's first exclusion, these surface the caller's environment, not an SDK defect. |

The heuristics are deliberately one-sided and conservative: a textual marker
demotes a site to `LIKELY_INELIGIBLE`, and everything else stays `CANDIDATE` —
including the positive signals the policy calls out, like `failed to marshal`,
`recovered panic`, or `unexpected`. Words like `unknown` and `malformed` are
deliberately **not** markers: in this repository they also fire on SDK
invariant failures the policy lists as reportable (`telemetry: unknown metric
type`, a malformed response we failed to parse). This is a triage aid, never an
eligibility verdict.

## Run

```sh
# Table output to stdout, grouped by owning team
make errtrack-audit

# Focus on one package (prefix match against the path relative to the module root)
(cd scripts/errtrackaudit && GOWORK=off go run . -root ../.. -package ddtrace/tracer)

# JSON for further processing
(cd scripts/errtrackaudit && GOWORK=off go run . -root ../.. -format json) > /tmp/audit.json
```

## Suppressing reviewed sites

Once a site has been reviewed, annotate it with `//errtrack:ignore` and a
reason, and it drops out of the audit while still being counted in the
per-owner totals:

```go
log.Error("agent unreachable: %s", err.Error()) //errtrack:ignore — user environment, not our defect
```

Directive semantics: the comment must sit on a line spanned by the call itself
— a trailing comment on the call's first line, or a comment between a
multi-line call's opening and closing parenthesis. A standalone comment line
above the call does **not** suppress it. This is a standalone directive rather
than a `//nolint:errtrack` entry: `errtrackaudit` is not a golangci-lint
linter, so naming it in a `//nolint:` comment makes golangci-lint's nolint
filter warn about an unknown linter on every run.

A site that has adopted `ReportError` alongside its plain `log.Error` line
should carry the directive too (with the adopting PR as the reason), so the
`CANDIDATE` count shrinks as migration PRs land:

```go
log.Error("failed to flush trace chunks: %s", err.Error()) //errtrack:ignore — reported via ReportError in #5251
telemetrylog.ReportError("failed to flush trace chunks", err)
```

Sites rewritten to `LogAndReportError` disappear from the audit on their own,
since the helper does the logging.

## CI

The `.github/workflows/errtrack-audit.yml` workflow runs the audit on PRs
touching root-module code and uploads the table and JSON as an artifact. It
does **not** fail the build — its job is to keep a per-team progress dashboard
in the PR comments as the migration proceeds.

# CI Workflows

Detailed reference for dd-trace-go's CI: what each workflow checks, which
workflows run on a given pull request, CODEOWNERS pattern rules, and how to
reproduce checks locally. See [CONTRIBUTING.md](./CONTRIBUTING.md) for the
short, contributor-facing overview that links here.

## CI Workflows

Our CI pipeline includes several automated checks:

### Static Checks Workflow

- **Copyright Check**: Verifies all files have proper copyright headers
- **CODEOWNERS Check**: Runs `scripts/check_codeowners.go` to verify every tracked file has an owner in [CODEOWNERS](./CODEOWNERS), and that GitHub and CI Visibility resolve that owner identically. The repository has no catch-all `*` entry, so a new top-level directory or root-level file is unowned until an entry is added. See [CODEOWNERS patterns](#codeowners-patterns) for the accepted syntax. Run locally with `make lint/misc`.
- **Generate Check**: Ensures generated code is up-to-date
- **Module Check**: Validates Go module consistency using `make fix-modules`
- **Lint Check**: Runs comprehensive linting using `golangci-lint`
- **Error-logging Lint**: Runs `make lint/errlog`, three `go vet`-compatible analyzers: `constantlogmsg` (rejects non-constant message arguments on `log.Error`, `log.Warn`, and the `telemetrylog.ReportError`/`ReportPanic`/`LogAndReportError`/`LogAndReportPanic` helpers — non-constant messages break dedup and, for the telemetry-reporting functions, risk leaking PII to Error Tracking), `telemetrysafety` (requires `slog.Any`/`slog.String` values passed to telemetry log calls to be PII-safe), and `logformatverbs` (flags unsafe `%v`/`%+v`/`%#v` usage). Run locally with `make lint/errlog`. Before adding a new `ReportError`/`ReportPanic` call site, read "When to report, and when not to" in [`internal/README.md`](./internal/README.md#telemetry) — the short version: our defect (not the caller's environment or externally-controlled input), swallowed, not per-span, and firing on the tracer's own startup/poll path rather than a customer request. `internal/telemetry/log/report_backend_test.go` and `internal/telemetry/telemetrytest.NewCapturingClient` decode the real wire payload offline, so a unit test can already assert the message, error type, stack trace, and dedup count for a new call site. Before merging one, still dogfood it against a real org with [`internal/apps/telemetry-errors`](./internal/apps/telemetry-errors/README.md) — that's for what a unit test genuinely can't verify: whether the production telemetry intake accepts the payload (tier 1) and whether the report actually lands and is searchable in the product (tier 2).
- **Lock Analysis**: Runs `checklocks` to detect potential deadlocks and race conditions
- **Cross-Compile Check**: Runs `scripts/cross_build.sh` to cross-compile the library for every [first class Go port](https://go.dev/wiki/PortingPolicy) (including 32-bit `linux/386`, `windows/386`, `linux/arm`), catching architecture-specific compile regressions. Run locally with `./scripts/cross_build.sh`. Packages that import `go-libddwaf` are skipped until it builds on 32-bit (see DataDog/go-libddwaf#227); they stay covered on 64-bit by the test matrix.

### Unit and Integration Tests

- **Core Tests**: Tests the main library functionality, including that specific Error Tracking call
  sites (remote-config update-state JSON-parse errors in internal/remoteconfig, and the OTel-process-
  context site in internal/apps/telemetry-errors) produce well-formed telemetry payloads, and that
  sites which deliberately do *not* report (decision-maker parsing in ddtrace/tracer, verified by
  `TestParseDecisionMaker_MalformedValue_LogsLocallyWithoutReporting`; and storeConfig's memfd site in
  internal/apps/telemetry-errors, both externally-triggerable/customer-environment conditions rather
  than SDK defects) still log locally without reporting — see internal/apps/telemetry-errors/README.md
  for the full dogfooding process these regression tests automate tier 0 of.
- **Integration Tests**: Tests against real services using Docker
- **Contrib Tests**: Tests all third-party integrations
- **Race Detection**: Tests with Go race detector enabled

### Generate Workflow

- **Code Generation**: Ensures all generated code is current and consistent

### Config Audit Workflow

- **Config Audit**: Runs `make config-audit` to report the migration status of each `DD_*` environment-variable configuration relative to `internal/config`. The check is non-blocking — it does not prevent a PR from merging, but posts the audit results as a PR comment. Run locally with `make config-audit`.

### Customer Simulation Platform (CuSim)

- **CuSim Deployment**: Scheduled GitLab `deploy_to_reliability_env` (from the one-pipeline template) runs deploy [all Go apps](https://github.com/DataDog/datadog-reliability-env/tree/master/apps/go) to CuSim using the latest dd-trace-go release (`released`), the HEAD of `main` (`candidate`), and custom configurations (`experimental`). The job can be triggered by anyone, but CuSim resources are only accessible to Datadog internal contributors.

## Which checks run on a pull request

Most workflows only run when a change could plausibly affect them. Two mechanisms
do this, and which one applies is recorded in
[`.github/ci-components.yml`](./.github/ci-components.yml):

- A **`changes` job** classifies the pull request diff and the workflow's other
  jobs gate on its outputs with `if:`. Skipped jobs still report a check run, and
  the green-CI gate counts a `skipped` conclusion as a pass.
- A native **`on.pull_request.paths`** filter, for workflows scoped to one narrow
  area. A filtered-out workflow reports no check run at all.

`.github/ci-components.yml` maps every path in the repository to the CI work it
requires. It is ordered and first-match-wins, and the one rule that matters is:

> **A path matching no component enables every gate.**

The table is an allowlist of paths that are provably safe to skip, not a denylist
of expensive ones. A new directory is therefore fully tested by default, and
`make lint/misc` fails until someone classifies it — the same no-catch-all
discipline as [CODEOWNERS](#codeowners-patterns).

Some things worth knowing before editing the table:

- **Directory names are a poor proxy for impact.** Every `contrib/` module
  transitively imports 90-100 root-module packages, so a `ddtrace/tracer` change
  really does need all of them. Conversely `datastreams/options` is reachable from
  77 modules while the rest of `datastreams/` is reachable from 8. Check with
  `go list -deps` rather than guessing from the path.
- **`contrib/os/` has no `go.mod`.** It is root-module code living under a
  `contrib/` path, so it escalates to the full suite.
- The `dependent-modules` lists are measured facts, re-derived nightly by
  `go test -tags depgraph ./scripts/ciselect/` in the `Main Branch and Release
  Tests` workflow.
- The merge queue is not gated. Everything gating a pull request still runs in
  full on `mq-working-branch-*` before a commit can land on `main`.

To see what a change set resolves to, without pushing:

```shell
git diff --name-only origin/main...HEAD | go run ./scripts/ciselect -explain
```

If a workflow was skipped and you believe it should have run, that is a bug in
the table — open an issue or fix the component. To restore full CI for a
component without touching any workflow, set its `gates:` to `[ "@everything" ]`.

## Running CI Checks Locally

Before submitting a PR, you can run the same checks locally using make targets:

```shell
# Show all available targets
make help

# Install tools
make tools-install

# Run all linters (same as CI)
make lint

# Format code (recommended before committing)
make format

# Check module consistency
make fix-modules

# Run all tests
make test

# Run integration tests
make test-integration
```

You can also run scripts directly for more control:

```shell
# Run specific linting options
make lint

# Format specific file types
make format/go
make format/shell

# Run specific test configurations
make test/contrib
make test/appsec
```

## Development Scripts

We provide several utility scripts in the `scripts/` directory to help with common development tasks:

### Code Quality Scripts

#### `make lint`

Runs all linters on the codebase to ensure code quality and consistency.

```shell
# Run all linters (default behavior, install tools)
make lint
```

The script runs:

- `goimports` for import formatting
- `golangci-lint` for comprehensive Go linting
- `checklocks` for lock analysis (with error tolerance)

#### `make format`

Formats Go and shell files in the repository.

```shell
# Format both Go and shell files and install tools (default behavior)
make format

# Format Go files and install tools
make format/go

# Format shell files and install tools
make format/shell
```

#### `./scripts/checklocks.sh`

Analyzes lock usage patterns to detect potential deadlocks and race conditions.

```shell
# Install the managed checklocks binary
make tools-install

# Run checklocks on the default target (./ddtrace/tracer)
./scripts/checklocks.sh

# Run checklocks on a specific directory
./scripts/checklocks.sh ./path/to/target

# Run checklocks and ignore known issues
./scripts/checklocks.sh --ignore-known-issues
```

### Module Management Scripts

#### `make fix-modules`

Maintains Go module consistency across the repository by running `go mod tidy` on all modules and adding missing replace directives for local imports.

```shell
make fix-modules
```

This script:

- Runs the `fixmodules` tool to add missing replace directives
- Executes `go mod tidy` on all Go modules in the repository
- Updates the `go.work.sum` file

### Testing Scripts

#### `make test`

Enhanced testing script with improved output formatting and additional options.

```shell
# Run core tests only
make test/unit

# Run integration tests
make test/integration

# Run contrib tests
make test/contrib

# Run all tests
make test

# Run with AppSec enabled
make test/appsec
```

The script provides:

- Timestamped output for better debugging
- Early failure detection with clear error messages
- Automatic Docker service management for integration tests
- Support for Apple Silicon (M1/M2) Macs

#### Crashtracker

Run focused crashtracker tests with:

```shell
go test -race -count=1 ./crashtracker
```

The end-to-end tests intentionally crash helper processes and validate the
report received by a local intake stub.

## Docker Alternative

If you prefer using Docker for linting:

```shell
docker run --rm -v $(pwd):/app -w /app golangci/golangci-lint:v1.63.3 golangci-lint run -v --timeout 5m
```

## CODEOWNERS patterns

[CODEOWNERS](./CODEOWNERS) is read by two consumers that do not implement the same matching rules: GitHub, which follows gitignore semantics, and CI Visibility, which uses the simpler matcher in [internal/civisibility/utils](./internal/civisibility/utils/codeowners.go) to attribute test results to teams. A pattern the two interpret differently assigns the right reviewers while silently mis-attributing test ownership.

To keep them in agreement, entries are restricted to the subset on which both behave identically:

| Pattern | Meaning |
| --- | --- |
| `/path/to/dir/` | Anchored at the repository root, applies to everything beneath the directory. The trailing slash is required. |
| `/path/to/file.go` | Anchored at the repository root, matches exactly one file. |
| `*suffix` | Matches any path ending in `suffix`, at any depth. Only one leading `*` is supported, and the suffix may not contain `/`. |

Later entries take precedence over earlier ones. There is no catch-all `*` entry: every path is owned explicitly, so **a new top-level directory or root-level file needs a new entry**. `make lint/misc` fails otherwise.

## Benchmarks

Some benchmarks will run on any new PR commits, the results will be commented into the PR on completion.

### Adding a new benchmark

To add a benchmark that runs on every PR, edit [`.gitlab/benchmarks/micro/gitlab-ci.yml`](./.gitlab/benchmarks/micro/gitlab-ci.yml)
and append your top-level benchmark function name (e.g. `BenchmarkMyThing`) to the `BENCHMARKS` variable of one of the
`microbenchmarks-N` groups, using the pipe character (`|`) as the separator.

A few things to keep in mind:

- The value is a top-level benchmark function name (`func BenchmarkMyThing(b *testing.B)`), not a sub-benchmark. It is
  matched with `go test -bench ^BenchmarkMyThing$`, so all of its `b.Run` sub-benchmarks run and are reported individually.
- The benchmark must live in a package that isn't excluded by the runner (it skips `orchestrion`, `civisibility`,
  `scripts`, and `tools`).
- Keep at most `44 / CPUS_PER_BENCHMARK` entries per group (the groups run in parallel across the job's CPUs). Add your
  entry to the smallest group, or create a new `microbenchmarks-N` group if they are full.
- Only `microbenchmarks-1` and `microbenchmarks-2` feed the `pr-performance-gates` job, so a benchmark placed in another
  group is measured and tracked but does not gate the PR.
- A benchmark that is new relative to `main` has no baseline, so the runner skips its comparison on the introducing PR and
  starts gating it from the next PR onward.
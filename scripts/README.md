# Development Scripts

This directory contains scripts and small Go tool programs used for development, testing, and maintenance of the dd-trace-go project.

## Script Types

### Shell Scripts

- Bash scripts for common development tasks
- Can be run directly or via Makefile targets
- Automatically use development tools from `bin/` directory

### Go Programs

- Small utility programs for specific development tasks
- Should have appropriate build tags to avoid being built by `go build`:
  - `//go:build ignore` and `// +build ignore`
  - `//go:build tools` and `// +build tools`
  - `//go:build scripts` and `// +build scripts`
- Development modules should **not** be included in the `go.work` file

## Usage

### Via Makefile (Recommended)

The Makefile provides convenient targets that automatically handle tool dependencies:

[embedmd]:# (../tmp/make-help.txt)
```txt
Usage: make [target]

Targets:
  help                 Show this help message
  all                  Run complete build pipeline (tools, generate, lint, test)
  tools-install        Install development tools
  tools-install/checkmake Install checkmake binary for Makefile linting
  clean                Clean build artifacts
  clean-all            Clean everything including tools and temporary files
  generate             Run code generation
  lint                 Run linting checks
  lint/go              Run Go linting checks
  lint/go/fix          Fix linting issues automatically
  lint/shell           Run shell script linting checks
  lint/misc            Run miscellaneous linting checks (copyright, Makefiles)
  lint/action          Lint GitHub Actions workflows
  lint/errlog          Run SDK logging safety analyzers — constant messages, SafeError/LogValuer telemetry scrubbing, unsafe %v format verbs
  format               Format code
  format/go            Format Go code
  format/shell         install shfmt
  test                 Run all tests (core, integration, contrib)
  test/unit            Run unit tests
  test/appsec          Run tests with AppSec enabled
  test/contrib         Run contrib package tests
  test/integration     Run integration tests
  test-deadlock        Run tests with deadlock detection
  test-debug-deadlock  Run tests with debug and deadlock detection
  fix-modules          Fix module dependencies and consistency
  fix/go               Apply go fix modernizations to Go code
  fix/go/diff          Preview go fix modernizations (dry-run)
  apidiff              Run semantic API diff for ddtrace/tracer against main
  apidiff/incompatible Show only breaking (incompatible) API changes for ddtrace/tracer
  docs                 Generate and Update embedded documentation in README files
  upgrade/orchestrion  Upgrade Orchestrion and fix modules
  config-audit         Report which DD_* configs are migrated to internal/config
```

### Direct Execution

Scripts can be run directly, but ensure development tools are available:

```bash
# Install tools first
make tools-install

# Run script with correct PATH
PATH="$(pwd)/bin:$PATH" ./scripts/script-name.sh

# Or run directly if script doesn't need tools
./scripts/script-name.sh
```

#### Test Script Options

The test script provides many options for different testing scenarios:

[embedmd]:# (../tmp/test-help.txt)
```txt
test.sh - Run the tests for dd-trace-go
  this script requires gotestsum, goimports, docker and docker-compose.
  -a | --appsec      - Test with appsec enabled
  -i | --integration - Run integration tests. This requires docker and docker-compose. Resource usage is significant when combined with --contrib
  -c | --contrib     - Run contrib tests
  --all              - Synonym for -l -a -i -c
  -s | --sleep       - The amount of seconds to wait for docker containers to be ready - default: 30 seconds
  -t | --tools       - Install gotestsum and goimports
  -h | --help        - Print this help message

Environment Variables:
  BUILD_TAGS         - Comma-separated Go build tags (e.g., BUILD_TAGS=deadlock or BUILD_TAGS=debug,deadlock)
```

### Go Programs

Build and run Go programs in the scripts directory:

```bash
# Build and run a Go script
go run -tags scripts ./scripts/program-name.go

# Or if it has ignore tags
go run ./scripts/program-name.go
```

## Adding New Scripts

### Shell Scripts

1. Create the script in the `scripts/` directory
2. Make it executable: `chmod +x scripts/script-name.sh`
3. Add a Makefile target if it's commonly used (follow the pattern of existing targets)
4. Use `$(BIN_PATH)` in Makefile targets to access development tools from `bin/`

### Go Programs

1. Create the Go file in appropriate subdirectory
2. Add proper build tags to prevent inclusion in main builds
3. If it needs dependencies, create a separate `go.mod` file
4. Don't add the module to `go.work`

## Build Metrics Scripts

Scripts for measuring build cost and publishing to Datadog CI Visibility:

### measure_build.sh

Measures build time and binary size for Orchestrion integration samples. Builds are performed with a cold build cache to measure full compilation cost, after warming the module download cache (untimed) so the measurement reflects compilation rather than network downloads.

```bash
# Build with standard Go toolchain
./scripts/measure_build.sh --sample net_http --mode standard --output /tmp/metrics.json

# Build with Orchestrion
./scripts/measure_build.sh --sample net_http --mode orchestrion --output /tmp/metrics.json

# Multiple repeats for median (reduces noise)
./scripts/measure_build.sh --sample net_http --mode standard --repeats 3
```

**Options:**
- `--sample NAME` - Sample to build (default: net_http)
- `--mode MODE` - Build mode: `standard` or `orchestrion` (required)
- `--output PATH` - Output JSON file path (default: stdout)
- `--repeats N` - Number of build repeats (default: 3)

**Output format:**
```json
{
  "sample": "net_http",
  "mode": "orchestrion",
  "metrics": {
    "build_duration_samples": [312.4, 308.1, 315.7],
    "binary_size_bytes": 48217344
  },
  "go_version": "1.25.0",
  "orchestrion_version": "v1.9.0"
}
```

`build_duration_samples` contains one entry per `--repeats` run. `binary_size_bytes` is taken from the last build.

### publish_build_metrics.sh

Publishes build metrics to Datadog CI Visibility using `datadog-ci`. Attaches measures (`go.build.duration_seconds`, `go.build.duration_seconds.0`, `go.build.duration_seconds.1`, ..., `go.build.binary_size_bytes`, and, in standard mode with dependency attribution, `go.build.dependency_size_bytes.<dep>`, `go.build.top_dependency_size_bytes.0`, `go.build.top_dependency_size_bytes.1`, ...) and tags (`build.toolchain`, `build.sample`, `build.cache`, `go.version`, `orchestrion.version`, and, in standard mode, `build.top_dependency_name.0`, `build.top_dependency_name.1`, ...) to the current CI job span.

Each attributed dependency is published as a measure named after it (`go.build.dependency_size_bytes.<dep>`) for querying one dependency's size trend over time. It's also published by rank — `go.build.top_dependency_size_bytes.<i>` paired with a `build.top_dependency_name.<i>` tag holding that rank's dependency name, where index `0` is the single largest dependency in that build.

```bash
# Set environment and publish
export METRICS_FILE=/tmp/metrics.json
export DATADOG_API_KEY=<key>
export DATADOG_SITE=datadoghq.com
./scripts/publish_build_metrics.sh
```

**Required environment variables:**
- `METRICS_FILE` - Path to metrics JSON from `measure_build.sh`
- `DATADOG_API_KEY` - Datadog API key
- `DATADOG_SITE` - Datadog site (default: datadoghq.com)

## CI Timing Scripts

Scripts for measuring CI durations and Go build-cache behaviour from
completed GitHub Actions runs. GitHub completed-run data is the source of
truth; Datadog series are secondary.

### citiming

`go run ./scripts/citiming` collects deduplicated observations (one record
per run, attempt, and job) plus one PR feedback record per PR revision,
and compares two collected windows using baseline-frozen strata and
baseline-frequency weights.

Collection requires an authenticated `gh` with read access to workflow
runs, jobs, logs, check runs, and the cache API. Raw job logs are input
data only: they are never executed or republished in full.

```bash
# Collect one window of completed runs (daily cadence during measurement
# programs; collects logs before they expire). Evidence stays outside the
# source tree.
go run ./scripts/citiming collect \
  --repo DataDog/dd-trace-go \
  --since 2026-09-14 --until 2026-09-20 \
  --output-dir /tmp/ci-timing/baseline-week-1 \
  --events pull_request --cache-snapshot

# Compare two windows (offline, no API access needed).
go run ./scripts/citiming compare \
  --baseline /tmp/ci-timing/baseline-week-1 \
  --candidate /tmp/ci-timing/candidate-week-1 \
  --output-dir /tmp/ci-timing/comparison
```

Metric definitions (job wall time including post steps, post time, restore
and save result classification, PR feedback time including the all-green
delay, and the exact accepted check-name exclusions) live in the doc
comment of `scripts/citiming/main.go` and are the report contract.

Restores are classified from the structured `cache-observation:` record
that `.github/actions/setup-go` prints into every job log: exact, prefix,
cold_miss, disabled, error, or unknown. Saves are classified per event from
completed log markers (`Cache saved with key:`, `Cache hit occurred on the
primary key ... not saving cache`, reservation conflicts, failures); a
successful job never counts as a successful save on its own.

The filesystem sizes published by `.github/actions/cache-metrics` are
end-of-job uncompressed usage under the `ci.cache.disk_size_bytes` series
with `phase:end_of_job` (the former `ci.step.cache.restore.disk_size_bytes`
name was misleading and is retired). Compressed cache-service storage is
measured from the cache API's `size_in_bytes` in `cache_snapshot.json`.

Tests live in `scripts/citiming/citiming_test.go`:

```bash
go test -race -count=1 ./scripts/citiming/
```

### Weekly review procedure

1. Collect each day of the window before job logs expire, with
   `--cache-snapshot`.
2. Compare only windows with the same frozen workload strata; a comparison
   needs at least five independent successful first-attempt run IDs per
   stratum per window, and five comparable PR revisions per window for PR
   feedback. Extend the window rather than triggering runs to reach a
   count.
3. Do not pool different runner classes, Go patch versions, build modes, or
   contrib module selections; the compare tool keeps strata separate.
4. Record failures, retries, cancellations, and unknown classifications
   from the reports; skipped jobs are absent work, not zero durations.
5. Publish aggregates and run URLs in the PR description, never raw logs.

## Guidelines

- Scripts should be idempotent when possible
- Include error handling and clear output messages
- Document any external dependencies (Docker, etc.)
- Use development tools from `bin/` directory when available
- Keep scripts focused on single responsibilities

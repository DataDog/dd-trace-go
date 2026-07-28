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

Publishes build metrics to Datadog CI Visibility using `datadog-ci`. Attaches measures (`go.build.duration_seconds.0`, `go.build.duration_seconds.1`, ..., `go.build.binary_size_bytes`) and tags (`build.toolchain`, `build.sample`, `build.cache`, `go.version`, `orchestrion.version`) to the current CI job span. Each element of `build_duration_samples` is published as an individually-indexed measure.

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

## Guidelines

- Scripts should be idempotent when possible
- Include error handling and clear output messages
- Document any external dependencies (Docker, etc.)
- Use development tools from `bin/` directory when available
- Keep scripts focused on single responsibilities

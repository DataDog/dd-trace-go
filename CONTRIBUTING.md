# Contributing

Thanks for your interest in contributing! This is an open source project, so we appreciate community contributions.

Pull requests for bug fixes are welcome, but before submitting new features or changes to current functionalities [open an issue](https://github.com/DataDog/dd-trace-go/issues/new)
and discuss your ideas or propose the changes you wish to make. After a resolution is reached a PR can be submitted for review. PRs created before a decision has been reached may be closed.

For commit messages, try to use the same conventions as most Go projects, for example:

```text
contrib/database/sql: use method context on QueryContext and ExecContext

QueryContext and ExecContext were using the wrong context to create
spans. Instead of using the method's argument they were using the
Prepare context, which was wrong.

Fixes #113
```

Please apply the same logic for Pull Requests and Issues: start with the package name, followed by a colon and a description of the change, just like
the official [Go language](https://github.com/golang/go/pulls).

All new code is expected to be covered by tests.

## Continuous Integration on Pull Requests

We expect all PR checks to pass before we merge a PR.

The code coverage report has a target of 90%. This is the goal, but is not a hard requirement. Reviewers ultimately make the decision about code coverage and quality and will merge PRs at their discretion. Any divergence from the expected 90% should be communicated by the reviewers to the PR author.

Please feel free to comment on a PR if there is any difficulty or confusion about any of the checks.

### CI Workflows

Our CI pipeline includes several automated checks:

#### Static Checks Workflow

- **Copyright Check**: Verifies all files have proper copyright headers
- **Generate Check**: Ensures generated code is up-to-date
- **Module Check**: Validates Go module consistency using `./scripts/fix_modules.sh`
- **Lint Check**: Runs comprehensive linting using `golangci-lint`
- **Lock Analysis**: Runs `checklocks` to detect potential deadlocks and race conditions

#### Unit and Integration Tests

- **Core Tests**: Tests the main library functionality
- **Integration Tests**: Tests against real services using Docker
- **Contrib Tests**: Tests all third-party integrations
- **Race Detection**: Tests with Go race detector enabled

#### Generate Workflow

- **Code Generation**: Ensures all generated code is current and consistent

### CI Troubleshooting

Sometimes a pull request's checks will show failures that aren't related to its changes. When this happens, you can try the following steps:

1. Look through the GitHub Action logs for an obvious cause
2. Retry the test a few times to see if it flakes
3. For internal contributors, ask the #dd-trace-go channel for help
4. If you are not an internal contributor, [open an issue](https://github.com/DataDog/dd-trace-go/issues/new/choose) or ping @Datadog/apm-go

### Running CI Checks Locally

Before submitting a PR, you can run the same checks locally:

```shell
# Run all linters (same as CI)
./scripts/lint.sh

# Format code (recommended before committing)
./scripts/format.sh --all

# Check module consistency
./scripts/fix_modules.sh

# Run all tests
./scripts/test.sh --all

# Run integration tests
./scripts/test.sh --integration
```

## Getting a PR Reviewed

We try to review new PRs within a week of them being opened. If more than two weeks have passed with no reply, please feel free to comment on the PR to bubble it up.

If a PR sits open for more than a month awaiting work or replies by the author, the PR may be closed due to staleness. If you would like to work on it again in the future, feel free to open a new PR and someone will review.

## Development Scripts

We provide several utility scripts in the `scripts/` directory to help with common development tasks:

### Code Quality Scripts

#### `./scripts/lint.sh`

Runs all linters on the codebase to ensure code quality and consistency.

```shell
# Run all linters (default behavior)
./scripts/lint.sh

# Install linting tools only
./scripts/lint.sh --tools

# Run all linters and install tools
./scripts/lint.sh --all
```

The script runs:

- `goimports` for import formatting
- `golangci-lint` for comprehensive Go linting
- `checklocks` for lock analysis (with error tolerance)

#### `./scripts/format.sh`

Formats Go and shell files in the repository.

```shell
# Format Go files only (default behavior)
./scripts/format.sh

# Format Go files and install tools
./scripts/format.sh --go

# Format shell files and install tools
./scripts/format.sh --shell

# Format both Go and shell files and install tools
./scripts/format.sh --all

# Install formatting tools only
./scripts/format.sh --tools
```

#### `./scripts/checklocks.sh`

Analyzes lock usage patterns to detect potential deadlocks and race conditions.

```shell
# Run checklocks on the default target (./ddtrace/tracer)
./scripts/checklocks.sh

# Run checklocks on a specific directory
./scripts/checklocks.sh ./path/to/target

# Run checklocks and ignore errors
./scripts/checklocks.sh --ignore-errors
```

### Module Management Scripts

#### `./scripts/fix_modules.sh`

Maintains Go module consistency across the repository by running `go mod tidy` on all modules and adding missing replace directives for local imports.

```shell
./scripts/fix_modules.sh
```

This script:

- Runs the `fixmodules` tool to add missing replace directives
- Executes `go mod tidy` on all Go modules in the repository
- Updates the `go.work.sum` file

### Testing Scripts

#### `./scripts/test.sh`

Enhanced testing script with improved output formatting and additional options.

```shell
# Run core tests only
./scripts/test.sh

# Run integration tests
./scripts/test.sh --integration

# Run contrib tests
./scripts/test.sh --contrib

# Run all tests
./scripts/test.sh --all

# Run with AppSec enabled
./scripts/test.sh --appsec

# Install test tools
./scripts/test.sh --tools

# Run specific test with race detection
./scripts/test.sh --race

# Run with custom sleep time for service startup
./scripts/test.sh --sleep 30
```

The script provides:

- Timestamped output for better debugging
- Early failure detection with clear error messages
- Automatic Docker service management for integration tests
- Support for Apple Silicon (M1/M2) Macs

## Style Guidelines

A set of [Style guidelines](https://github.com/DataDog/dd-trace-go/wiki/Style-guidelines) was added to our Wiki. Please spend some time browsing it.
It will help tremendously in avoiding comments and speeding up the PR process.

### Local Development

For local development, you can use the provided scripts instead of running tools directly:

```shell
# Instead of running golangci-lint directly
./scripts/lint.sh

# Instead of running formatters manually
./scripts/format.sh --all

# Instead of running go mod tidy manually
./scripts/fix_modules.sh
```

### Docker Alternative

If you prefer using Docker for linting:

```shell
docker run --rm -v $(pwd):/app -w /app golangci/golangci-lint:v1.63.3 golangci-lint run -v --timeout 5m
```

## Code quality

### Favor string concatenation and string builders over fmt.Sprintf and its variants

[fmt.Sprintf](https://pkg.go.dev/fmt#Sprintf) can introduce unnecessary overhead when building a string. Favor [string builders](https://pkg.go.dev/strings#Builder), or simple string concatenation, `a + "b" + c` over `fmt.Sprintf` when possible, especially in hot paths.
Sample PR: <https://github.com/DataDog/dd-trace-go/pull/3365>

### Integrations

Please view our contrib [README.md](contrib/README.md) for information on integrations. If you need support for a new integration, please file an issue to discuss before opening a PR.

### Adding Go Modules

When adding a new dependency, especially for `contrib/` packages, prefer the minimum secure versions of any modules rather than the latest versions. This is to avoid forcing upgrades on downstream users for modules such as `google.golang.org/grpc` which often introduce breaking changes within minor versions.

This repository used to omit many dependencies from the `go.mod` file due to concerns around version compatibility [(ref)](https://github.com/DataDog/dd-trace-go/issues/810). As such, you may have configured git to ignore changes to `go.mod` and `go.sum`. To undo this, run

```shell
git update-index --no-assume-unchanged go.*
```

### Uprading Go Modules

Please also see the section about "Adding Go modules" when it comes to selecting the minimum secure versions of a module rather than the latest versions.

Then start by updating the main `go.mod` file, e.g. by running a `go get` command in the root of the repository like this:

```
go get <import-path>@<new-version>
```

Then run the following script in order to update all `go.mod` and `go.sum` files in the repository:

```
./scripts/fix_modules.sh
```

This is neccessary because dd-trace-go is a multi-module repository.

### Benchmarks

Some benchmarks will run on any new PR commits, the results will be commented into the PR on completion.

#### Adding a new benchmark

To add additional benchmarks that should run for every PR, go to `.gitlab-ci.yml`.
Add the name of your benchmark to the `BENCHMARK_TARGETS` variable using pipe character separators.

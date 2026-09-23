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

## Pull Request Naming

Pull requests should follow [conventional commits](https://www.conventionalcommits.org/en/v1.0.0/) naming format with the following structure:

```text
<type>(scope): <description>
```

Where:

- **type**: The type of change (feat, fix, docs, style, refactor, test, chore)
- **scope**: The package or area affected (e.g., contrib/database/sql, ddtrace/tracer)
- **description**: A brief description of the change

Examples:

- `feat(contrib/http): add support for custom headers`
- `fix(ddtrace/tracer): resolve memory leak in span processor`


All new code is expected to be covered by tests. A regression test for a bug fix should fail against the pre-fix code.

## Continuous Integration on Pull Requests

We expect all PR checks to pass before we merge a PR.

The code coverage report has a target of 90%. This is the goal, but is not a hard requirement. Reviewers ultimately make the decision about code coverage and quality and will merge PRs at their discretion. Any divergence from the expected 90% should be communicated by the reviewers to the PR author.

Please feel free to comment on a PR if there is any difficulty or confusion about any of the checks.

For what each CI workflow checks, which workflows run on a given diff, CODEOWNERS pattern rules, and how to reproduce checks locally, see [ci-workflows.md](./docs/ci-workflows.md).

### CI Troubleshooting

Sometimes a pull request's checks will show failures that aren't related to its changes. When this happens, you can try the following steps:

1. Look through the GitHub Action logs for an obvious cause
2. Retry the test a few times to see if it flakes
3. For internal contributors, ask the #dd-trace-go channel for help
4. If you are not an internal contributor, [open an issue](https://github.com/DataDog/dd-trace-go/issues/new/choose) or ping @DataDog/apm-go

## Getting a PR Reviewed

We try to review new PRs within a week of them being opened. If more than two weeks have passed with no reply, please feel free to comment on the PR to bubble it up.

If a PR sits open for more than a month awaiting work or replies by the author, the PR may be closed due to staleness. If you would like to work on it again in the future, feel free to open a new PR and someone will review.

## Style Guidelines

A set of [Style guidelines](https://github.com/DataDog/dd-trace-go/wiki/Style-guidelines) was added to our Wiki. Please spend some time browsing it.
It will help tremendously in avoiding comments and speeding up the PR process.

### Comments

Add comments only for non-obvious intent, trade-offs, or constraints the code can't carry. Don't narrate what the diff already shows.

## Code quality

### Favor using internal implementations over external

When possible, prioritize creating or using internal implementations for repetitive work instead of importing a new dependency. The tracer already supports replacements for common Go packages. For example:

1. Logging: [internal/log](./internal/log) instead of `fmt`.
2. Locking: [internal/locking](./internal/locking) instead of `sync.mutex`.
3. OS: [internal/env](./internal/env) instead of `os.Getenv`. This is also available at [instrumentation/env](./instrumentation/env/) for those packages that cannot import internal modules.
4. Errors: [instrumentation/errortrace](./instrumentation/errortrace/) instead of `errors`.

### Concurrency and shared state

Any change that touches shared mutable state, adds or modifies a `sync.Once`-guarded teardown path, or calls into user-supplied callbacks (samplers, hooks, options) while holding a lock must include a test exercising the concurrent path under `-race`. When auditing a `sync.Once`-guarded shutdown, check every piece of shared state touched during teardown, not just the first step — a partial guard is a common source of races. Use the `checklocks` tool (see [Lock Analysis](./docs/ci-workflows.md#static-checks-workflow) and `./scripts/checklocks.sh`) to catch these before review.

### Favor string concatenation and string builders over fmt.Sprintf and its variants

[fmt.Sprintf](https://pkg.go.dev/fmt#Sprintf) can introduce unnecessary overhead when building a string. Favor [string builders](https://pkg.go.dev/strings#Builder), or simple string concatenation, `a + "b" + c` over `fmt.Sprintf` when possible, especially in hot paths.
Sample PR: <https://github.com/DataDog/dd-trace-go/pull/3365>

### Integrations

Please view our contrib [README.md](contrib/README.md) for information on integrations. If you need support for a new integration, please file an issue to discuss before opening a PR.

### Working with environment variables

When working with environment variables, direct use of `os.Getenv` and `os.LookupEnv` is not permitted. Instead, all environment variables must be validated against an [allowed list](./internal/env/supported_configurations.gen.go) using `env.Get` and `env.Lookup` from the [`internal/env`](./internal/env.go) package (or [`instrumentation/env`](./instrumentation/env/env.go) when working on contrib packages). This validation system helps us automatically detect newly introduced variables and ensures they are properly documented and tracked.

For how to add a new variable to the registry, existing override variables (`DD_CIVISIBILITY_FLAKY_RETRY_ENABLED`, `DD_CODE_COVERAGE_FLAGS`), and how to resolve related CI failures, see [environment-variables.md](./docs/environment-variables.md).

### Adding Go Modules

When adding a new dependency, especially for `contrib/` packages, prefer the minimum secure versions of any modules rather than the latest versions. This is to avoid forcing upgrades on downstream users for modules such as `google.golang.org/grpc` which often introduce breaking changes within minor versions.

This repository used to omit many dependencies from the `go.mod` file due to concerns around version compatibility [(ref)](https://github.com/DataDog/dd-trace-go/issues/810). As such, you may have configured git to ignore changes to `go.mod` and `go.sum`. To undo this, run

```shell
git update-index --no-assume-unchanged go.*
```

### Upgrading Go Modules

Please also see the section about "Adding Go modules" when it comes to selecting the minimum secure versions of a module rather than the latest versions.

Then start by updating the main `go.mod` file, e.g. by running a `go get` command in the root of the repository like this:

```
go get <import-path>@<new-version>
```

Then run the following commands to update all `go.mod` and `go.sum` files in the repository:

```
make fix-modules
make generate
```

This is neccessary because dd-trace-go is a multi-module repository.

### Benchmarks

Some benchmarks run automatically on new PR commits, with results commented into the PR. See [benchmarks-and-debugging.md](./docs/benchmarks-and-debugging.md) for how to add a new benchmark and how the gating groups work.

### Goroutine Leaks

Some core packages use [uber-go/goleak](https://github.com/uber-go/goleak) to detect goroutine leaks. See [benchmarks-and-debugging.md](./docs/benchmarks-and-debugging.md) for how to isolate and debug a leak, including CI-only leaks and unhelpful stack traces.

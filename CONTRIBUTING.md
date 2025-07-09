### Contributing

Thanks for your interest in contributing! This is an open source project, so we appreciate community contributions.

Pull requests for bug fixes are welcome, but before submitting new features or changes to current functionalities [open an issue](https://github.com/DataDog/dd-trace-go/issues/new)
and discuss your ideas or propose the changes you wish to make. After a resolution is reached a PR can be submitted for review. PRs created before a decision has been reached may be closed.

For commit messages, try to use the same conventions as most Go projects, for example:
```
contrib/database/sql: use method context on QueryContext and ExecContext

QueryContext and ExecContext were using the wrong context to create
spans. Instead of using the method's argument they were using the
Prepare context, which was wrong.

Fixes #113
```
Please apply the same logic for Pull Requests and Issues: start with the package name, followed by a colon and a description of the change, just like
the official [Go language](https://github.com/golang/go/pulls).

All new code is expected to be covered by tests.

#### Continuous Integration on Pull Requests

We expect all PR checks to pass before we merge a PR.

The code coverage report has a target of 90%. This is the goal, but is not a hard requirement. Reviewers ultimately make the decision about code coverage and quality and will merge PRs at their discretion. Any divergence from the expected 90% should be communicated by the reviewers to the PR author.

Please feel free to comment on a PR if there is any difficulty or confusion about any of the checks.

Sometimes a pull request's checks will show failures that aren't related to its changes. When this happens, you can try the following steps:

1. look through the gitlab job logs for an obvious cause
2. retry the test a few times to see if it flakes
3. for internal contributors, ask the #dd-trace-go channel for help
4. if you are not an internal contributor, [open an issue](https://github.com/DataDog/dd-trace-go/issues/new/choose) or ping @Datadog/apm-go

#### Getting a PR Reviewed

We try to review new PRs within a week of them being opened. If more than two weeks have passed with no reply, please feel free to comment on the PR to bubble it up.

If a PR sits open for more than a month awaiting work or replies by the author, the PR may be closed due to staleness. If you would like to work on it again in the future, feel free to open a new PR and someone will review.

### Style guidelines

A set of [Style guidelines](https://github.com/DataDog/dd-trace-go/wiki/Style-guidelines) was added to our Wiki. Please spend some time browsing it.
It will help tremendously in avoiding comments and speeding up the PR process.

To run golangci-lint locally:

```
docker run --rm -v $(pwd):/app -w /app golangci/golangci-lint:v1.63.3 golangci-lint run -v --timeout 5m
```

### Code quality

#### Favor string concatenation and string builders over fmt.Sprintf and its variants

[fmt.Sprintf](https://pkg.go.dev/fmt#Sprintf) can introduce unnecessary overhead when building a string. Favor [string builders](https://pkg.go.dev/strings#Builder), or simple string concatenation, `a + "b" + c` over `fmt.Sprintf` when possible, especially in hot paths.
Sample PR: https://github.com/DataDog/dd-trace-go/pull/3365

### Integrations

Please view our contrib [README.md](contrib/README.md) for information on integrations. If you need support for a new integration, please file an issue to discuss before opening a PR.

### Adding Go Modules

When adding a new dependency, especially for `contrib/` packages, prefer the minimum secure versions of any modules rather than the latest versions. This is to avoid forcing upgrades on downstream users for modules such as `google.golang.org/grpc` which often introduce breaking changes within minor versions.

This repository used to omit many dependencies from the `go.mod` file due to concerns around version compatibility [(ref)](https://github.com/DataDog/dd-trace-go/issues/810). As such, you may have configured git to ignore changes to `go.mod` and `go.sum`. To undo this, run

```
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

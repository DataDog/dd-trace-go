### Contributing

Pull requests for bug fixes are welcome, but before submitting new features or changes to current functionalities [open an issue](https://github.com/DataDog/dd-trace-go/issues/new)
and discuss your ideas or propose the changes you wish to make. After a resolution is reached a PR can be submitted for review.

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

#### PR Checks

We expect all PR checks to pass before we merge a PR. When opening a PR, the metadata check will fail until a repo maintainer assigns the PR a milestone. The other checks can be investigated by following the `Details` links to CircleCI and CodeCov for unit/integration tests and code coverage checks.

![Screen Shot 2021-08-31 at 10 35 37 AM](https://user-images.githubusercontent.com/1819836/131533266-7c87305d-37df-4bd5-a9ea-6fb8e51e4b50.png)

The code coverage report has a target of 90%. This is the goal, but is not a hard requirement. Reviewers ultimately make the decision about code coverage and quality and will merge PR's at their discretion. Any divergence from the expected 90% should be communicated by the reviewers to the PR author.

Please feel free to comment on a PR if there is any difficulty or confusion about any of the checks.

### Style guidelines

A set of [Style guidelines](https://github.com/DataDog/dd-trace-go/wiki/Style-guidelines) was added to our Wiki. Please spend some time browsing it.
It will help tremendously in avoiding comments and speeding up the PR process.


### Integrations

Please view our contrib [README.md](contrib/README.md) for information on new integrations.

### Go Modules

When adding a new dependency, especially for `contrib/` packages, prefer the minimum secure versions of any modules rather than the latest versions. This is to avoid forcing upgrades on downstream users for modules such as `google.golang.org/grpc` which often introduce breaking changes within major versions.

This repository used to omit many dependencies from the `go.mod` file due to concerns around version compatibility [(ref)](https://github.com/DataDog/dd-trace-go/issues/810). As such, you may have configured git to ignore changes to `go.mod` and `go.sum`. To undo this, run

```
git update-index --no-assume-unchanged go.*
```

### Milestones

The maintainers of this repository assign milestones to pull requests to classify them. `Triage` indicates that it is yet to be decided which version the change will go into. Pull requests that are ready get the upcoming release version assigned.

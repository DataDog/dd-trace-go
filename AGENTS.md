# Project overview

## Read CONTRIBUTING.md First

**BEFORE reviewing, writing, or editing ANY code**, you MUST read [CONTRIBUTING.md](./CONTRIBUTING.md) and [README.md](./README.md).

Furthermore, be sure to follow [Effective Go guidelines](https://go.dev/doc/effective_go) when writing Go code.

Run required verification commands directly. If you pipe their output, enable `pipefail` so an earlier failure cannot be masked.

Before reviewing or changing files in these areas, read the applicable scoped instructions:

* [contrib/AGENTS.md](./contrib/AGENTS.md) -- for updating contribs/integrations
* [ddtrace/tracer/AGENTS.md](./ddtrace/tracer/AGENTS.md) -- for updating core Datadog tracer implementations and features
* [internal/AGENTS.md](./internal/AGENTS.md) -- for updating features and implementations that are not customer facing
* [profiler/AGENTS.md](./profiler/AGENTS.md) -- for profiling updates

## CI scripts

Every `curl` that runs in CI (GitHub Actions workflows and composite actions, GitLab jobs, and the scripts, Makefile targets and Dockerfiles they use) must:

* **Fail on HTTP errors**: pass `-f`/`--fail`. Without it, a 4xx/5xx exits 0 and the error page is used as if it were the real response.
* **Retry transient errors**: pass `--retry 5 --retry-delay 2 --retry-connrefused` instead of hand-written retry blocks.
* **Propagate the failure**: use `pipefail` for `curl | tar`/`curl | jq`, and never run `bash -c "$(curl ...)"` or `curl ... | sh` without `-f`: download to a file first.

Example: `curl -fsSL --retry 5 --retry-delay 2 --retry-connrefused -o out.tar.gz "$url"`

Exception: a call that checks the HTTP status itself (`-w '%{http_code}'`, e.g. the test agent trace check) may skip `--fail`, but still needs `--retry`.

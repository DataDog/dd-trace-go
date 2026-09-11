# Project overview

## Read CONTRIBUTING.md First

**BEFORE reviewing, writing, or editing ANY code**, you MUST read [CONTRIBUTING.md](./CONTRIBUTING.md) and [README.md](./README.md).

Furthermore, be sure to follow [Effective Go guidelines](https://go.dev/doc/effective_go) when writing Go code.

Run required verification commands directly. If you pipe their output, enable `pipefail` so an earlier failure cannot be masked.

Before reviewing or changing files in these areas, read the applicable scoped instructions:

* [contrib/AGENTS.md](./contrib/AGENTS.md) -- for updating contribs/integrations
* [ddtrace/tracer/AGENTS.md](./ddtrace/tracer/AGENTS.md) -- for updating core Datadog tracer implementations and features
* [internal/AGENTS.md](./internal/AGENTS.md) -- for updating features and implementations that are not customer facing
* [orchestrion/AGENTS.md](./orchestrion/AGENTS.md) -- for updating or creating new Orchestrion (auto-instrumentation) files
* [profiler/AGENTS.md](./profiler/AGENTS.md) -- for profiling updates

## Review Guidelines

**Local agent with a skill harness:** Before pushing code or creating a pull request, you must run
the [dd-apm-sdk-review](./.agents/skills/dd-apm-sdk-review/) skill on your unpushed changes. If any
`P0` issues are reported, you must either fix them or get explicit authorization from the human you
are working with and record the unresolved finding verbatim in the PR description. `P1` and `P2`
findings should be fixed before pushing, but can be dismissed by the human.

Exception: security findings are never pasted into a PR description — a PR is a public forum, so
posting one there is an improper disclosure. Route them privately.

**Reviewer without a skill harness** (for example, GitHub Codex): read and follow
`.agents/skills/dd-apm-sdk-review/review-without-harness.md`. Do not load `SKILL.md`
or `reviewers/report-template.md`.


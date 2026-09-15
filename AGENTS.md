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

**Local agent with a skill harness:** Run the [dd-apm-sdk-review](./.agents/skills/dd-apm-sdk-review/) skill on demand when asked. It is not required before every push.

Verdicts (P0 / P1 / P2, BLOCK / APPROVE) live in
[`.agents/skills/dd-apm-sdk-review/reviewers/_common.md`](./.agents/skills/dd-apm-sdk-review/reviewers/_common.md)
— do not restate them here.

If a human authorizes shipping with an unresolved P0, record that finding verbatim in the PR
description.

**Reviewer without a skill harness** (for example, GitHub Codex): read and follow
`.agents/skills/dd-apm-sdk-review/review-without-harness.md`. Do not load `SKILL.md`
or `reviewers/report-template.md`.


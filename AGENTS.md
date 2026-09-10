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

**Reviewer without a skill harness** (GitHub Codex, or any agent that cannot invoke
`.agents/skills/`): do not run the skill and do not execute `SKILL.md` Step 1. When you are
reviewing a pull request or a diff, use these files as the review spec — the checks and the
P0/P1/P2 bar only:

- `.agents/skills/dd-apm-sdk-review/reviewers/_common.md` (always)
- `.agents/skills/dd-apm-sdk-review/reviewers/coherence.md`
- `.agents/skills/dd-apm-sdk-review/reviewers/correctness.md`
- `.agents/skills/dd-apm-sdk-review/reviewers/security.md`
- `.agents/skills/dd-apm-sdk-review/reviewers/design.md`
- `.agents/skills/dd-apm-sdk-review/reviewers/performance.md`
- `.agents/skills/dd-apm-sdk-review/reviewers/maintainability.md`
- `.agents/skills/dd-apm-sdk-review/reviewers/conventions.md`
- `.agents/skills/dd-apm-sdk-review/reviewers/cross-sdk.md`
- the matching file under `.agents/dd-apm-sdk-review-overrides/reviewers/` when it exists
  (additive; read both)
- `.agents/dd-apm-sdk-review-overrides/repo-context.md` (cite related skills; do not invoke them)

Do not load `SKILL.md` or `reviewers/report-template.md`. Ignore harness-only rules in the files you
do load: do not emit `READY TO PUSH` / `DO NOT PUSH` / `WAITING ON HUMAN`, and the `_common.md` rule
"Never post to GitHub" does not apply to you — post findings as review comments. Skip a lens that
cannot apply to this diff rather than inventing a finding.

If this change set is only agent-instruction files (`.agents/`, `AGENTS.md`, `CLAUDE.md`), review
that prose for broken paths and contradictions. Do not apply the product lenses to the instruction
text.


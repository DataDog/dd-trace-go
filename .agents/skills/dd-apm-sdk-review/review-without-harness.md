# Review without a skill harness

Use this file when you cannot invoke `.agents/skills/` (GitHub Codex, or any
pull-request review bot). Do not run `dd-apm-sdk-review` and do not execute
`SKILL.md` Step 1. This file is the review contract, not a product lens.

Paths below are relative to the tracer repository root after this file is
mirrored to `.agents/skills/dd-apm-sdk-review/`.

When you are reviewing a pull request or a diff, use these files as the
review spec — the checks and the P0/P1/P2 bar only:

- `.agents/skills/dd-apm-sdk-review/reviewers/_common.md` (always)
- `.agents/skills/dd-apm-sdk-review/reviewers/coherence.md`
- `.agents/skills/dd-apm-sdk-review/reviewers/correctness.md`
- `.agents/skills/dd-apm-sdk-review/reviewers/security.md`
- `.agents/skills/dd-apm-sdk-review/reviewers/design.md`
- `.agents/skills/dd-apm-sdk-review/reviewers/performance.md`
- `.agents/skills/dd-apm-sdk-review/reviewers/maintainability.md`
- `.agents/skills/dd-apm-sdk-review/reviewers/conventions.md`
- `.agents/skills/dd-apm-sdk-review/reviewers/cross-sdk.md`
- the matching file under `.agents/dd-apm-sdk-review-overrides/reviewers/`
  when it exists (additive; read both)
- `.agents/dd-apm-sdk-review-overrides/repo-context.md` when it exists
  (cite related skills; do not invoke them)

Do not load `SKILL.md` or `reviewers/report-template.md`. Ignore
harness-only rules in the files you do load: do not emit `READY TO PUSH` /
`DO NOT PUSH` / `WAITING ON HUMAN`, and the `_common.md` rule "Never post
to GitHub" does not apply to you — post findings as review comments. Skip a
lens that cannot apply to this diff rather than inventing a finding.

If this change set is only agent-instruction files (`.agents/`, `.claude/`,
`.cursor/`, `AGENTS.md`, `CLAUDE.md`), review that prose for broken paths
and contradictions. Do not apply the product lenses to the instruction text.

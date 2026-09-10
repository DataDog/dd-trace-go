# Repo context — dd-trace-go

Read only by the orchestrator (Step 0 of `SKILL.md`), not by individual reviewers. Repo-specific; not part of the shared core. This whole `.agents/dd-apm-sdk-review-overrides/` folder is owned by this repo — edit it freely, unlike `.agents/skills/dd-apm-sdk-review/`, which is a verbatim copy of the shared core.

## Related skills in this repo

The other skills in this repo author specific things; this one is the general multi-perspective push gate. Cite them as authoritative for their own area, do not invoke them, and note they must not invoke this skill either:

- `documentation-policy` — where a significant feature, `make` option, CI workflow, or scoped `AGENTS.md` must be documented (`CONTRIBUTING.md`, package docs, nearest README). Defer for "did they update the docs?" questions.

Claude commands live under `.claude/commands/` (`checklocks`). Cite a command as the standard for its own check; do not invoke this skill from it.

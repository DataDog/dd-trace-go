# Consolidated report format and verdict table

Used by the orchestrator (`SKILL.md`, Step 3) to shape the final report. Language-agnostic — identical across every repo that adopts this skill.

## Verdict table

| verdict | condition | gate effect |
|---|---|---|
| `BLOCK` | ≥1 P0 finding | `DO NOT PUSH` |
| `APPROVE_WITH_COMMENTS` | P1 and/or P2 only | `WAITING ON HUMAN` — show the findings and ask whether to fix or dismiss; do not say `READY TO PUSH` or `DO NOT PUSH` until the human answers. Fixes preferred; only the human may dismiss. |
| `APPROVE` | nothing to raise | `READY TO PUSH` |

A reviewer that could not do its job reports `NOT VERIFIED (<reason>)` for its area. `NOT VERIFIED` never blocks.

## Report format

The **gate line is the first thing a human must see** — same line as the title, or the line immediately under it. Do not bury `DO NOT PUSH` / `WAITING ON HUMAN` / `READY TO PUSH` under empty headings.

**Omit every section that has nothing to say.** An empty `## P0`, `## P1`, `## P2`, `## Not verified`, `## Checked and fine`, or `## Coverage gaps` heading is noise. If the only thing you would print is a placeholder, drop the section.

Findings stay above the `---`. On `APPROVE_WITH_COMMENTS`, the P1/P2 list **and** the fix-or-dismiss question go **above** the `---`, right under the gate line — not after Rule files / Related skills.

```
# dd-apm-sdk-review: <repo name> — DO NOT PUSH | WAITING ON HUMAN | READY TO PUSH

Verdict: BLOCK | APPROVE_WITH_COMMENTS | APPROVE
Target: <branch>...<base>   Files: <n>   Mode: parallel | sequential | DEGRADED | pasted diff, no git

## P0
- [design] path/to/file.ext:123 — <issue>
  Failure mode: <what breaks, for whom, when>
  Fix: <concrete change>

## P1
- [design] path/to/file.ext:45 — <issue> → <suggested fix>

## P2
- [conventions] path/to/file.ext:9 — <issue>

## Not verified
- [cross-sdk] NOT VERIFIED (no spec source available)

On APPROVE_WITH_COMMENTS, ask here (before the ---):
Tell me whether to fix or dismiss each finding. If you fix, this skill re-runs every reviewer.

---
## Rule files used
<!-- One line per lens in SKILL.md Step 2's roster: check whether THIS repo has an override for that lens, and append its path only if it does. -->
- coherence: reviewers/coherence.md
- correctness: reviewers/correctness.md
- design: reviewers/design.md<+ override path, or "(no override for this repo)">
- performance: reviewers/performance.md<+ override path, or "(no override for this repo)">
- maintainability: reviewers/maintainability.md<+ override path, or "(no override for this repo)">
- conventions: reviewers/conventions.md<+ override path, or "(no override for this repo)">
- cross-sdk: reviewers/cross-sdk.md

## Related skills in this repo
- <from `.agents/dd-apm-sdk-review-overrides/repo-context.md`, Step 0 - the other skills this repo has and how they relate to this review; omit this section if that file has none>

## Checked and fine
- [performance] no new allocations on the span-start path
- ...

## Coverage gaps
- <changed files with no test changes; missing release note; etc.>
```

The section below the `---` is bookkeeping for debugging the review itself — keep it after the findings, never before them. Still omit any of those bookkeeping sections that are empty.

Then state the gate line that matches the verdict table (already required in the title): `DO NOT PUSH` (`BLOCK`), `WAITING ON HUMAN` (`APPROVE_WITH_COMMENTS`), or `READY TO PUSH` (`APPROVE`).

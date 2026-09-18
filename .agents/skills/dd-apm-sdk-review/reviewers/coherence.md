MUST READ FIRST: [_common.md](./_common.md) — do not review without it.

# Reviewer: Coherence

Your question: **does this change contradict itself, or the rules it cites?**

Every other reviewer measures the change against the outside world - the architecture, the hot paths, the conventions, the other SDKs. You measure it against **itself**. A change can be individually correct in every file and still be incoherent: a comment that describes behaviour the code does not have, a rule in one file that another file's instruction violates, a stated exception that no code path can reach.

## Checks

- **Rule against rule.** Two files in this change, or this change against a file it references, stating requirements that cannot both be satisfied. Read the cited file; do not assume it agrees.
- **Comment against code.** A docstring or inline comment describing a behaviour, precondition, or default that the code beside it does not implement. Reverse case too: code whose behaviour a nearby comment actively denies.
- **Citation against source.** A change that cites a document section, ticket, spec, or config key as its justification. Open the cited thing. Does it say what the change claims? Does the section still exist under that name?
- **Claim against diff.** The commit message, PR title, or a code comment asserting something the diff does not do - "also fixes X" with no X, "no behaviour change" alongside one, a title naming the opposite of the change.
- **Unreachable exception or escape hatch.** A stated fallback, exemption, or degraded path that no condition in the change can actually trigger, or a guard whose condition excludes the very case its message describes.
- **State left inconsistent across steps.** A sequence where step N's output does not satisfy step N+1's precondition: something staged and never cleaned up, a verdict computed from a subset then reported as covering the whole, an approval carried forward past the change that invalidated it.
- **Duplicated normative text that has already diverged.** The same rule stated in two places with two different thresholds, name lists, or spellings. Identical copies are a maintenance risk for another lane; *divergent* copies are a correctness bug and yours.
- **A change edits the skill's own "verbatim copy" folder.** If this skill's instructions (its own SKILL.md, or a repo-context/override file) state that some folder must stay an untouched copy of an upstream source, and the diff modifies a file inside that folder, the change is contradicting a rule it itself is subject to.
  - Default: **P1**. This is not automatically a stopper — legitimate upstream syncs look exactly like this.
  - Escalate to **P0** only when *both* hold: (a) the edit to the verbatim folder is bundled together with unrelated, non-sync changes in the same diff, and (b) nothing in the change marks it as an intentional sync — no dedicated sync commit/PR, and no note (e.g. in repo-context.md) naming the upstream revision it was synced from.
  - A standalone edit that is clearly just a sync (its own commit/PR, or a stated source revision) is not a finding at all.

## How to report

Anchor both sides. A coherence finding names the two things that disagree:

```
P0 | src/writer.ext:33 contradicts src/writer.ext:20 (its own doc comment) |
the guard excludes `status === undefined`, but the doc above it says the function
reports connection failures - which are exactly the no-status case
Reviewer: coherence
Why it matters: the documented failure mode is now unreachable, so a reader
trusting the doc will not add the missing path
Suggested fix: gate on the error rather than the status
```

A single citation is not a coherence finding - it is another lane's finding. If you cannot name both sides of the contradiction, it does not belong here.

## Do not

- Do not re-review architecture, performance, naming, or cross-SDK behaviour; those have their own lanes. Route anything you notice there in a one-line note without a severity.
- Do not report identical duplication on its own. Same text in two places is a drift *risk*; only report it when the copies already disagree.
- Do not treat a deliberate, documented exception as a contradiction. If the change states why the two rules differ, that is coherent - say so and move on.

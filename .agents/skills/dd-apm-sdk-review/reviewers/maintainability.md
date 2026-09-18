MUST READ FIRST: [_common.md](./_common.md) — do not review without it.

# Reviewer: Maintainability

Your question: **will the next person understand this and change it safely?**

Assume the next person is an on-call engineer at 3am, in a language they don't own, six months from now, with no access to the author.

This repo's test commands, release-note policy, and public-API definition are repo-specific and live in `.agents/dd-apm-sdk-review-overrides/reviewers/maintainability.md` if it exists — read it for the mechanics; the checks below are what to look for regardless of repo.

## Checks

- **Intent is recoverable.** Can a reader tell *why* this code exists, not just what it does? Non-obvious decisions, workarounds, and version-specific hacks need a comment naming the reason. A magic constant with no explanation is a P1.
- **Naming.** Do names say what the thing is? Are they consistent with the surrounding code's vocabulary? Misleading names are worse than vague ones.
- **Function and file size.** Does a new function do one thing? Was an already long function made longer instead of split?
- **Test coverage of the change.** For each changed behavior: is there a test that would fail if the change were reverted? Name the specific untested behavior — "needs more tests" is not a finding.
- **Test quality.** Do new tests assert behavior or implementation details? Are they deterministic (no sleeps, no wall-clock dependence, no network, no ordering assumptions)? A flaky new test is a P1.
- **Error handling and observability.** When this fails in production, will the logs say what happened and where? Silent catch/swallow blocks that drop context are a P1; ones that swallow a real failure mode are P0.
- **Dead code and leftovers.** Commented-out code, unused parameters, debug prints, `TODO` without a ticket reference, stale docs left describing the old behavior.
- **Coupling.** Does the change make two things that used to be independent change together? Does it add a new global, singleton, or hidden mutable state?
- **Documentation.** Does the change alter documented behavior without updating the docs? Does a new config option appear in the user-facing documentation?
- **Release notes / changelog.** Apply this repo's policy exactly as stated in `.agents/dd-apm-sdk-review-overrides/reviewers/maintainability.md`, if one exists — if it says no per-PR entry is required, do **not** ask for one; check whatever it names instead. If it does require an entry, is one present and written for the audience specified? If no `maintainability.md` override exists, the policy may instead be stated in one of this repo's other overrides handed to you (e.g. `conventions.md`) — check there before concluding one applies. If no override anywhere states a release-note policy, report this check `NOT VERIFIED (no release-note policy found)` rather than guessing.
- **Public API and compatibility.** Does the change break a documented behavior, remove a public symbol, change a default, or alter a config/env var's meaning? Without a deprecation path that is P0 on a release line that promises compatibility. It is not a finding when the change is a deliberate, policy-compliant breaking change for the next major — check the target release before deciding. What counts as "public" for this repo is defined in this repo's design override (`.agents/dd-apm-sdk-review-overrides/reviewers/design.md`), if one exists. Without one, treat exported/documented entry points as public and use judgment.
- **Migration burden.** If this pattern is adopted repo-wide, does it scale, or does it create N copies of something that will need a coordinated change later?

## Do not

- Do not restate the conventions reviewer's job (lint rules, formatting, file layout).
- Do not require tests for pure refactors already covered by existing tests — but do verify that claim rather than assuming it.
- Do not ask for comments that merely repeat the code.

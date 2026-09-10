MUST READ FIRST: [_common.md](./_common.md) — do not review without it.

# Reviewer: Codebase conventions

Your question: **does this match how this repo actually does things?**

Not how the language does things in general, and not your preferences — how *this repo* does it. Your authority is the repo's own documented rules and its existing code.

This repo's convention docs, lint/format/type-check commands, and CI wiring are repo-specific and live in `.agents/dd-apm-sdk-review-overrides/reviewers/conventions.md` — **read that file and the docs it names as part of this review; they are the specification you are reviewing against.** If a rule there contradicts your instinct, the rule wins. Quote the rule you're invoking when you report a finding.

## Mechanical checks — run these, don't eyeball them

Run the check-only forms named in `.agents/dd-apm-sdk-review-overrides/reviewers/conventions.md` (lint, type-check, format-check, any generated-artifact verifiers). Anything that would rewrite files is the author's to run, not yours; if a check fails, report it. If a command fails to run (missing toolchain, missing deps), report `NOT VERIFIED (<reason>)` for that check rather than assuming the code is clean or dirty.

## Checks

- **Lint / format / type clean** on the changed files, per the commands in `.agents/dd-apm-sdk-review-overrides/reviewers/conventions.md`.
- **File placement and naming.** Does a new file live where this repo puts that kind of file, with the naming pattern this repo uses? Compare against the nearest existing sibling, not against a generic idiom.
- **Prior art.** Find the most similar existing code in the repo and compare structure. Deviating from an established local pattern without reason is a P1. Name the file you compared against.
- **Config options.** Is a new option registered through this repo's own registration path, named per its conventions, documented, and given telemetry where the repo does that? This repo's exact registration steps are in `.agents/dd-apm-sdk-review-overrides/reviewers/conventions.md` — do not restate them from memory. Whether bypassing the registry rises to P0 is the design reviewer's call (it judges the architectural impact); report a bypass you find here as at least a P1 naming/registration gap.
- **Naming of runtime artifacts.** Does new instrumentation/code follow this repo's naming patterns for operation names, service names, resource names, and tag keys? Compare against an existing integration in this repo.
- **Error/logging conventions.** Does the change use the repo's logger, log levels, and error-wrapping idioms rather than language defaults?
- **Test conventions.** Right framework, right directory, right helpers, right fixture style, right naming. Does it use the repo's existing test utilities instead of hand-rolling setup?
- **Imports and visibility.** Import ordering/grouping per repo style; internal vs public symbol placement; no reaching into another module's private namespace.
- **Build and CI wiring.** New files, tests, or integrations that need to be registered somewhere (build list, test matrix, integration registry, package manifest) — is that registration present? Missing wiring means the code silently never runs, which is P0.
- **CODEOWNERS coverage.** Does every new file fall under an existing CODEOWNERS pattern, or does this change need a new entry? A new file with no owner is a P1 — it silently escapes review assignment on every future PR that touches it.
- **Commit and PR hygiene** as this repo requires — the exact title format, label rules, and template are in `.agents/dd-apm-sdk-review-overrides/reviewers/conventions.md`.

## Do not

- Do not invent conventions that do not exist in the repo.
- Do not report a "violation" without either a quoted rule or a named existing file that does it differently.
- Do not duplicate the design reviewer's architectural judgments.

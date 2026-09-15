# Reviewer prompts

In [`dd-apm-sdk-review-core`](https://github.com/DataDog/dd-apm-sdk-review-core) this directory is the **source** — edit the rules here.

When these files are copied into a tracer repo (`.agents/skills/dd-apm-sdk-review/reviewers/`), that copy is a **mirror**. Edits there are overwritten and never propagate back. To change a review rule, open a PR against the source repo:
https://github.com/DataDog/dd-apm-sdk-review-core

Before contributing, please read:
- README: https://github.com/DataDog/dd-apm-sdk-review-core/blob/main/README.md
- How to contribute: https://github.com/DataDog/dd-apm-sdk-review-core/blob/main/CONTRIBUTING.md
- How testing works: https://github.com/DataDog/dd-apm-sdk-review-core/blob/main/docs/testing.md

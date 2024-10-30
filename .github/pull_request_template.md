<!--
* New contributors are highly encouraged to read our
  [CONTRIBUTING](/CONTRIBUTING.md) documentation.
* Commit and PR titles should be prefixed with the general area of the pull request's change.

-->
### What does this PR do?

<!--
* A brief description of the change being made with this pull request.
* If the description here cannot be expressed in a succinct form, consider
  opening multiple pull requests instead of a single one.
-->

### Motivation

<!--
* What inspired you to submit this pull request?
* Link any related GitHub issues or PRs here.
* If this resolves a GitHub issue, include "Fixes #XXXX" to link the issue and auto-close it on merge.
-->

### Reviewer's Checklist
<!--
* Authors can use this list as a reference to ensure that there are no problems
  during the review but the signing off is to be done by the reviewer(s).
-->

- [ ] Changed code has unit tests for its functionality at or near 100% coverage.
- [ ] [System-Tests](https://github.com/DataDog/system-tests/) covering this feature have been added and enabled with the va.b.c-dev version tag.
- [ ] There is a benchmark for any new code, or changes to existing code.
- [ ] If this interacts with the agent in a new way, a system test has been added.
- [ ] Add an appropriate team label so this PR gets put in the right place for the release notes.
- [ ] Non-trivial go.mod changes, e.g. adding new modules, are reviewed by @DataDog/dd-trace-go-guild.


Unsure? Have a question? Request a review!

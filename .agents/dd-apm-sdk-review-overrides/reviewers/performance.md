Override for `reviewers/performance.md` (in the core skill folder) — read that file first, then this.

# Performance — dd-trace-go specifics

This file should grow — add the next pattern you learn from review that is
**not** already written in a repo doc. Do not treat it as exhaustive.

The confirmed contrib span-tag pattern lives in
[`contrib/AGENTS.md` § "Span Tag Performance in Contribs"](../../../contrib/AGENTS.md).
Open that section and check the diff against it. Do not restate or narrow it
here — a fork of that rule in this file is how review guidance drifts from
the contributor doc.

`TestWithStartSpanConfigAliasesCachedBaseWhenCalledFirst` in
`ddtrace/tracer/option_test.go` reproduces the aliasing footgun that section
describes.

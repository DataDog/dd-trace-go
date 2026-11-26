# Design: fix `modernize` lint findings in `contrib/go-chi`

Status: proposal. This file is for review only. Delete it before merge.

## Decisions

| Decision | Selected option | Reasons |
|----------|-----------------|---------|
| Scope | Change only the 4 files in `contrib/go-chi/chi.v5` and `contrib/go-chi/chi` that have the 6 findings. Do not change the 38 other files with a `// import "..."` comment. | (1) The goal is a clean unfiltered lint in these 2 modules only. (2) The 38 other files are in other modules; a repo-wide change makes a large, noisy diff that touches many code owners. (3) CI lints only changed lines (`--new-from-rev`), so the 38 other files do not fail CI. A repo-wide cleanup is a separate change (see Follow-up). |
| Fix or suppress | Fix the code. Do not add `//nolint`. | (1) Each fix is a small, safe edit with the same behavior. (2) A `//nolint` keeps old syntax and adds noise. (3) The prior cleanup DataDog/dd-trace-go#5072 fixed findings, it did not suppress them. |
| `importcomment` edit | Remove only the ` // import "..."` text at the end of the `package chi` line. The line becomes `package chi`. Keep the package doc comment above it. | (1) In module mode the Go toolchain ignores this comment; `go.mod` sets the module path. (2) No exported API or import path changes. |
| `any` edit | In each `assertRate` closure, replace `rate interface{}` with `rate any`. Change nothing else. | (1) `any` is an alias of `interface{}` (Go 1.18+): the type is identical. (2) Test-only code. |
| `rangeint` edit | Drop the unused loop variables. `for n := 0; n < nbReqGoroutines; n++ {` becomes `for range nbReqGoroutines {`. `for i := 0; i < 100; i++ {` becomes `for range 100 {`. | (1) The loop bodies do not use `n` or `i` (checked at `contrib/go-chi/chi.v5/chi_test.go:591-600`). (2) `for n := range nbReqGoroutines` would cause an unused-variable compile error. `for _ = range` is redundant syntax. (3) Iteration count is the same (1000 × 100). (4) `for range <int>` is already used in 391 test lines in this repo. |
| Parity between `chi.v5` and `chi` | Apply the import comment edit and type alias edit to both modules. The loop edit applies to `chi.v5` only. | `contrib/go-chi/chi/chi_test.go` has no counted `for` loop (no `rangeint` finding). Adding a concurrency test to `chi` is out of scope. |
| Delivery | 1 PR, 1 commit for code (`chore(contrib/go-chi): fix modernize lint findings`), plus this file in a separate commit that is removed before merge. | The change is small and can be verified in one pass. |

## Problem

`golangci-lint run ./...` (unfiltered) fails in `contrib/go-chi/chi.v5` (4 findings) and `contrib/go-chi/chi` (2 findings) on `main` at `0a0dc5a4c`, with the CI-pinned version `v2.15.0`. CI does not fail because the nested-module lint uses `--new-from-rev`.

| File:line | Check | Fix (decision) |
|-----------|-------|----------------|
| `contrib/go-chi/chi.v5/chi.go:7` | `importcomment` | import comment edit |
| `contrib/go-chi/chi.v5/chi_test.go:342` | `any` | type alias edit |
| `contrib/go-chi/chi.v5/chi_test.go:591` | `rangeint` | loop edit |
| `contrib/go-chi/chi.v5/chi_test.go:596` | `rangeint` | loop edit |
| `contrib/go-chi/chi/chi.go:7` | `importcomment` | import comment edit |
| `contrib/go-chi/chi/chi_test.go:394` | `any` | type alias edit |

## Constraints

- No change to exported API (names, signatures, import path).
- No change to runtime behavior. Only `chi.go` line 7 changes in non-test code, and that line is a comment.
- Go 1.26 compatibility. All 4 modules set `go 1.26.0`; the CI floor is Go 1.26. `any` needs Go 1.18+; range over int needs Go 1.22+.
- Do not change `contrib/go-chi/chi.v5/otelc` or `contrib/go-chi/chi/otelc`. They already have 0 findings.
- Do not change any `go.mod` or `go.sum` file.
- Parity between `chi.v5` and `chi`, as in the parity decision.

## Options compared

### Scope

| Option | Benefit | Cost | Result |
|--------|---------|------|--------|
| A. Only the 4 `contrib/go-chi` files | Small diff, 1 code owner area, easy review | 38 other files keep the ignored comment | **Selected** |
| B. All 40 files with `// import` | Consistent repo state | Many modules and code owners; out of the stated goal; needs lint runs in every module | Rejected: out of scope |

### Fix or suppress

| Option | Benefit | Cost | Result |
|--------|---------|------|--------|
| A. Fix the code | Clean code, matches prior convention | None of note | **Selected** |
| B. `//nolint:modernize` on each line | No code change | Keeps old syntax, adds 6 suppression comments | Rejected |
| C. Disable `modernize` for these paths in `.golangci.yml` | 1 config edit | Hides future findings; changes repo config | Rejected |

### `rangeint` form

| Option | Compiles | Result |
|--------|----------|--------|
| A. `for range N {` | Yes | **Selected**: variables are not used |
| B. `for n := range N {` | No (unused variable) | Rejected |
| C. `for _ = range N {` | Yes, but `gofmt -s` simplifies it | Rejected: redundant |

## Verification

Run each command from the repository root. Use `golangci-lint` `v2.15.0` (the value of `GOLANGCI_LINT_VERSION` in `.github/workflows/static-checks.yml`).

```sh
golangci-lint --version   # must print 2.15.0
(cd contrib/go-chi/chi.v5 && golangci-lint run ./...)
(cd contrib/go-chi/chi    && golangci-lint run ./...)
(cd contrib/go-chi/chi.v5/otelc && golangci-lint run ./...)
(cd contrib/go-chi/chi/otelc    && golangci-lint run ./...)
(cd contrib/go-chi/chi.v5 && go test -race ./...)
(cd contrib/go-chi/chi    && go test -race ./...)
git diff --stat main -- ':!design-go-chi-modernize.md'
```

## Acceptance criteria

- [ ] `[lint]` `golangci-lint run ./...` with `v2.15.0`, without `--new-from-rev`, prints `0 issues.` in `contrib/go-chi/chi.v5`.
- [ ] `[lint]` Same command prints `0 issues.` in `contrib/go-chi/chi`.
- [ ] `[lint]` Same command still prints `0 issues.` in both `otelc` modules.
- [ ] `[unit test]` `go test -race ./...` passes in `contrib/go-chi/chi.v5` and `contrib/go-chi/chi`.
- [ ] `[ci]` The PR CI checks pass (static checks, unit tests).
- [ ] `[review]` The diff changes only the 4 files in scope (plus this file, which is deleted before merge).
- [ ] `[review]` No `//nolint` comment and no `.golangci.yml` change is added.
- [ ] `[review]` Each `chi.go` change is only the removal of ` // import "..."` on the `package chi` line.
- [ ] `[review]` Each `assertRate` change is only `interface{}` → `any`.
- [ ] `[review]` The 2 loops use `for range nbReqGoroutines {` and `for range 100 {`; loop bodies are not changed.
- [ ] `[review]` No `go.mod`, `go.sum`, or `otelc` file changes.
- [ ] `[review]` This design file is deleted before merge.

## Review requirements

1. The reviewer runs the 2 unfiltered lint commands with `v2.15.0` and records the output in the review.
2. The reviewer confirms the diff scope with `git diff --stat` against `main`.
3. The reviewer confirms that no exported symbol changes (only comment and test-file edits).
4. The PR description lists each check (`importcomment`, `any`, `rangeint`) with its files, and records lint and test results, as in DataDog/dd-trace-go#5072.

## Risks

| Risk | Mitigation |
|------|------------|
| A tool or doc depends on the `// import` comment | Module mode ignores it; `go.mod` sets the path. Other modules that already lack the comment build and publish normally. |
| Wrong loop count after the `rangeint` edit | The loop edit keeps the same bounds; `-race` test runs the loop. |
| Local linter version differs from CI | Verification step 1 checks the version. |

## Out of scope

- The 38 other files with a `// import "..."` comment.
- Any change to CI lint filtering (`--new-from-rev`).
- New tests, or a concurrency test for `contrib/go-chi/chi`.

## Follow-up

- Repo-wide `importcomment` cleanup of the 38 other files: makes the repo consistent; evidence: `grep -rlE '^package \w+ // import "' --include=*.go .` lists 40 files, all under `contrib/`.

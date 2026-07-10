# CI performance: where the time goes, and the real lever

## TL;DR

GitHub Actions step-level parallelism (`background` / `wait` / `wait-all`, added
2026-06) overlaps independent *steps within a job*. We applied it where it helps
(the workflows carrying the `# actionlint:skip-file parallel-steps` marker), but
the wins are small — seconds — because the slow jobs are each dominated by one
monolithic test step. The real lever for those is **job-level matrix sharding**,
above all for the contrib test suite.

## Why step-parallelism is capped here

A GitHub runner has a fixed core count and `go test` already uses them. Splitting
one test script into two background `go test` steps on the same runner just makes
them contend for the same cores. Step-parallelism only helps when steps wait on
*different* resources: network image pulls vs a CPU-bound build, an external token
fetch, an `npm install`. That is why only the `static-checks` `lint` job saw a
large win (two independent ~10-minute `golangci-lint` runs overlapped) and
everything else saw seconds.

## The dominant PR cost: the contrib test matrix

`unit-integration-tests.yml :: test-contrib-matrix` runs the
contrib/instrumentation suite. Its sharding is computed by
[`scripts/ci_contrib_matrix.go`](../scripts/ci_contrib_matrix.go):

- `numRunners = 6` (hardcoded). Packages are round-robined across 6 chunks, run
  per Go version (×2) → 12 parallel jobs.
- The file already carries the open question — *"can we find an optimal number of
  runners that will make the test efficient without creating too much cost?"* —
  and notes that the `APM Larger Runners` group shares ~50 runners.

Two structural inefficiencies, neither addressable by step-parallelism:

1. **Balanced by package count, not duration.** Round-robin assumes every package
   costs the same. It does not: a chunk that lands kafka + elasticsearch + mongo
   is far slower than a chunk of trivial packages. The slowest chunk gates the
   whole job, so unbalanced chunks waste the fast runners' time.
2. **Fixed shard count.** `6` is a guess, not a measured optimum.

## Proposed levers, in impact order

1. **Duration-aware sharding.** Replace round-robin-by-count with bin-packing by
   measured per-package test duration (longest-processing-time-first). Same runner
   budget, lower max-chunk wall-clock. Highest ROI, and it directly answers the
   TODO already in the file.
2. **Right-size `numRunners`.** With balanced chunks, sweep the shard count for the
   knee: per-job overhead (checkout + `setup-go` + tool build, ~1–2 min) sets a
   floor, so past some N more shards only add overhead and runner contention.
   Bounded by the ~50 shared-runner pool and cost.
3. **Confirm `go test` parallelism.** Ensure `-p` / `-parallel` in the test scripts
   match the runner core count, so each chunk saturates its runner before we add
   chunks.

## Measure first — do not tune blind

The pipeline already uploads test results and coverage to **Datadog CI
Visibility** (`./.github/actions/dd-ci-upload`). Before changing `numRunners` or
the sharding strategy, pull per-package and per-job durations from CI Visibility
to:

- find the actual long-pole packages,
- seed the duration table for bin-packing,
- pick the shard-count knee from data rather than by guessing.

## Scope

This is a separate effort from the parallel-steps change. It touches
`scripts/ci_contrib_matrix.go` and the test scripts, not workflow step syntax, and
it does not depend on the actionlint fix
([rhysd/actionlint#694](https://github.com/rhysd/actionlint/pull/694)).

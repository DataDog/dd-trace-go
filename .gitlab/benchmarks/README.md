# Benchmarks

This directory contains the configuration for the performance benchmarks that
run on the [Datadog Benchmarking Platform](https://datadoghq.atlassian.net/wiki/spaces/APMINT/pages/2419261562/Benchmarking+Platform)
via GitLab CI. Benchmarks are split into three complementary suites plus the
shared performance-gate configuration.

## Layout

- **`micro/`** — Go microbenchmarks (`go test -bench`) executed on every PR.
  Each PR is compared against a baseline (`main`) and the pipeline fails on
  regressions above the configured thresholds. See `micro/README.md` for how
  to add, run, or mark benchmarks as flaky, and how to run the pipeline
  locally with `bp-runner`.
- **`macro/`** — End-to-end macrobenchmarks that build the
  [`go-prof-app`](https://gitlab.ddbuild.io/DataDog/benchmarking-platform)
  SUT with the candidate `dd-trace-go` and exercise it under realistic load
  scenarios (`io-bound`, `cpu-bound`, `cgo-cpu-bound`, etc.). Runs
  automatically on `main` and release branches, and on-demand (manual) on
  feature branches. Used to detect regressions in tracer, profiler, AppSec,
  and service-extensions overhead.
- **`test-apps/`** — Long-running test applications (e.g. `unit-of-work`)
  used to capture profiles and validate behavior over extended runs
  (~10+ minutes). Triggered manually; useful for investigating issues that
  short benchmarks cannot reproduce.
- **`pr-gate.thresholds.yml`** — Regression thresholds enforced by the
  `pr-performance-gates` job for microbenchmark PR runs.
- **`pre-release-gate.slos.yml`** — SLOs evaluated before releases to gate
  promotion based on macrobenchmark results.

## External: `go-go-prof-app-parallel`

In addition to the suites under this directory, the top-level
`.gitlab-ci.yml` includes a shared pipeline definition from the
[`DataDog/apm-reliability/apm-sdks-benchmarks`](https://gitlab.ddbuild.io/DataDog/apm-reliability/apm-sdks-benchmarks)
project:

```yaml
- project: 'DataDog/apm-reliability/apm-sdks-benchmarks'
  file: '.gitlab/ci-go-go-prof-app-parallel.yml'
  ref: 'main'
```

This contributes the `go-go-prof-app-parallel` and
`go-go-prof-app-parallel-slo` stages, which run a next-generation,
parallelized variant of the `go-prof-app` macrobenchmarks. The job
definitions, SUT image, and SLOs are maintained centrally in
`apm-sdks-benchmarks` (shared across APM SDK repositories) rather than
in this repository. These benchmarks are intended to replace the legacy
`macro/` suite long term; until that migration completes, both suites
run side by side. To change thresholds, scenarios, or runner
configuration for this suite, open a PR against
`apm-sdks-benchmarks`.

## How it fits together

1. On every PR, `micro/gitlab-ci.yml` runs the microbenchmarks suite and
   compares results against `main` using `pr-gate.thresholds.yml`.
2. On `main` and release branches, `macro/gitlab-ci.yml` runs the
   macrobenchmarks suite; results are evaluated against
   `pre-release-gate.slos.yml` before a release is cut.
3. `test-apps/test-apps.yml` provides manual jobs for deeper investigation
   when micro/macro signals are not sufficient.
4. The `go-go-prof-app-parallel` and `go-go-prof-app-parallel-slo` stages run the next-generation parallel `go-prof-app`
   macrobenchmarks alongside the legacy `macro/` suite.

Each suite pins its own `BENCHMARKS_CI_IMAGE` and runs on dedicated
bare-metal GitLab runners to keep results stable:

- **Microbenchmarks** and **test-apps** use
  `registry.ddbuild.io/ci/benchmarking-platform:dd-trace-go-<job-id>`,
  built from `micro/container/` (a `golang` base plus `bp-runner` tooling
  installed via `bp-install`).
- **Macrobenchmarks** use a separate image:
  `486234852809.dkr.ecr.us-east-1.amazonaws.com/ci/benchmarking-platform:go-go-prof-app-and-serviceextensions-and-haproxy-<rev>`,
  which bundles the `go-prof-app` SUT, the Envoy service-extensions and
  HAProxy SPOA harnesses.

For details on running, extending, or debugging a specific suite, see the
README inside each subdirectory.

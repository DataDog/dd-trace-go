# Setup overview

The microbenchmarks pipeline runs Go benchmarks (`go test -bench`) inside the
Datadog Benchmarking Platform and compares results against a baseline to
detect performance regressions on every PR.

Files in this directory:

- **`gitlab-ci.yml`** — GitLab CI entrypoint. Defines the `microbenchmarks-N`
  jobs (one per group of benchmarks), runs them on dedicated 48-core
  bare-metal runners (`runner:apm-k8s-m8g-metal`), and chains the
  `pr-performance-gates` job that fails the pipeline on regressions above
  `regression_threshold` (see `../pr-gate.thresholds.yml`). Key variables:
  `BENCHMARKS` (pipe-separated benchmark names), `CPUS_PER_BENCHMARK`,
  `REPETITIONS`, `FLAKY_BENCHMARKS_REGEX`, `SIGNIFICANT_IMPACT_THRESHOLD`.
- **`bp-runner.yml`** — [bp-runner](https://github.com/DataDog/benchmarking-platform-tools/tree/main/bp-runner)
  experiment definition. Describes how to fetch the repo at the target
  commit, parallelize benchmarks across CPU cores (cores 4-47, leaving 0-3
  for kernel/background tasks), execute each benchmark `REPETITIONS` times
  with `go test -bench`, and analyze results for `GoBench` framework.
- **`container/`** — Dockerfile and install scripts for the CI image. Based on official `golang` image plus bp-runner tooling installed via `bp-install`.
- **`.env.example`** — Template for local-run environment variables (copy to
  `.env`).

Execution flow on each PR:

1. GitLab CI starts one `microbenchmarks-N` job per group from `gitlab-ci.yml`.
2. Each job invokes `bp-runner bp-runner.yml`, which runs the candidate
   commit and the baseline (`main`) for each benchmark in `BENCHMARKS`.
3. The `analyze_microbenchmarks` step parses results and uploads them to S3
   and the BP API.
4. The `pr-performance-gates` job evaluates regressions against
   `pr-gate.thresholds.yml` and posts a PR comment.

# How to add a new microbenchmark

1. Write the benchmark function in the appropriate package, following Go's
   standard `func BenchmarkXxx(b *testing.B)` convention. Make sure it lives in
   a `*_test.go` file. See [`BenchmarkStartSpan`](../../../ddtrace/tracer/tracer_test.go#:~:text=func%20BenchmarkStartSpan)
   in `ddtrace/tracer/tracer_test.go` as an example.

2. Verify it runs locally:

   ```bash
   go test -run=XXX -bench ^BenchmarkStartSpan$ -benchmem -count 1 -benchtime 2s ./ddtrace/tracer/...
   ```

3. In order to run benchmark in CI, register the benchmark name in `gitlab-ci.yml` by appending it to the
   `BENCHMARKS` variable of one of the `microbenchmarks-N` jobs (pipe-separated).
   Keep at most **44 scenarios per group** when using **CPUS_PER_BENCHMARK=1** (one CPU core per scenario, and cores 0-3 are reserved for kernel & background tasks). If a group is full, add it to another group or create a new
   `microbenchmarks-N` job that extends `.microbenchmarks`.

You can test the pipeline locally (see below) before opening a PR. 

New benchmarks
   that don't exist on the baseline branch are automatically skipped from
   baseline comparison on the first run.

# How to mark a benchmark as known flaky

If a benchmark is known to produce unstable results and shouldn't fail the
PR performance gate, add its name to the `FLAKY_BENCHMARKS_REGEX` variable
in `gitlab-ci.yml`. The value is a regular expression matched against
benchmark names; use `|` to separate multiple entries.

```yaml
# Known flaky benchmarks
FLAKY_BENCHMARKS_REGEX: "BenchmarkOTLPTraceWriterFlush|BenchmarkXxx"
```

The benchmark will still run and its results will be reported, but
regressions won't fail the pipeline.

Before marking a benchmark as flaky, you can confirm the instability by running a
stability test. See the
[`stability` scripts in benchmarking-platform-tools](https://github.com/DataDog/benchmarking-platform-tools/tree/main/scripts/stability)
for instructions on how to measure run-to-run variance.

# How to rebuild the CI Docker image

The `BENCHMARKS_CI_IMAGE` used by the microbenchmarks pipelines
is built from Dockerfile in `.gitlab/benchmarks/micro/container/`. You need to rebuild and update
the pinned tag whenever you change any of files in that directory (e.g. bumping the Go
base image, adding system packages, or updating `bp-runner` / `bp-analyzer` /
`bp-github` versions).

Steps:

1. Edit the relevant file(s) under `.gitlab/benchmarks/micro/container/`. Push
   the branch and open a PR.

2. The pipeline includes
   [`build-ci-images.template.yml`](https://github.com/DataDog/benchmarking-platform-tools/blob/main/images/templates/gitlab/build-ci-images.template.yml). It auto-detects changes under
   `BASE_CI_IMAGE_CONTAINER_DIR` (`.gitlab/benchmarks/micro/container`) and
   adds a `build-base-ci-image` job on Gitlab that build and pushes the image to registry.

3. Open the pipeline on
   [`gitlab.ddbuild.io/DataDog/apm-reliability/dd-trace-go`](https://gitlab.ddbuild.io/DataDog/apm-reliability/dd-trace-go)
   and run the `build-base-ci-image` job. Wait for it to finish successfully
   and copy the job URL and the resulting image tag from its logs.

4. Update both files to point at the newly built image (replace the job URL
   in the comment and the image tag):

   - `.gitlab/benchmarks/micro/gitlab-ci.yml`
   - `.gitlab/benchmarks/test-apps/test-apps.yml`

   ```yaml
   # Benchmarks image is created here: <paste build-base-ci-image job URL>
   BENCHMARKS_CI_IMAGE: registry.ddbuild.io/ci/benchmarking-platform:dd-trace-go-<NEW_PIPELINE_ID>
   ```

5. Push the update. The microbenchmarks will now run
   against the rebuilt image. Verify a green pipeline before merging.

# How to test microbenchmarks CI Docker image locally

Install [bp-runner](https://github.com/DataDog/benchmarking-platform-tools/blob/main/bp-runner/INSTALL.md), if you don't have it yet.

Then copy-paste `.env.example` to `.env`, and update configuration as needed.
When needed, CI branch & commit can be customized in `bp-runner.yml` file.

Then run:

```bash
bp-runner bp-runner.yml --debug -t --local
```

In order to run interactively, use:

```bash
bp-runner bp-runner.yml --debug -t --local -i

## and then inside container run, you can use
export GO_VERSION=$(go version | sed 's/go version //') && bp-runner bp-runner.yml --debug
```

### I have "Permission denied (publickey)." on git clone in Docker container build

Primarily ensure that the SSH agent on your host machine is running and that the SSH key is added to it.

For example, run new ssh agent and add your key to it:

```bash
eval $(ssh-agent -s) 
ssh-add ~/.ssh/<YOUR_KEY_NAME_HERE>
ssh-add -l
```

Ensure `ssh-add -l` command should show key fingerprint that matches the one in your Github account.

If this doesn't help, check Github docs on troubleshooting SSH key setup https://docs.github.com/en/authentication/troubleshooting-ssh/error-permission-denied-publickey.

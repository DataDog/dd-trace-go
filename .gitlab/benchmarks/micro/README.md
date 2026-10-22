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
4. The `pr-performance-gates` job waits for every benchmark group to finish its
   result uploads, evaluates regressions against `pr-gate.thresholds.yml`, and
   posts a PR comment.

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
   `microbenchmarks-N` job that extends `.microbenchmarks`. Add each new group to
   the `pr-performance-gates` job's `needs` list. Run `go test ./scripts/ciselect/`
   from the repository root to check that no group is missing.

You can test the pipeline locally (see below) before opening a PR. 

New benchmarks
   that don't exist on the baseline branch are automatically skipped from
   baseline comparison on the first run.

# How to check benchmark stability

Use the Benchmarking Platform stability workflow to compare repeated measurements
of identical code. Run this check before removing a benchmark from quarantine.
The [stability runbook](https://github.com/DataDog/benchmarking-platform-tools/blob/main/scripts/stability/README.md)
describes the workflow and its analysis settings.

## 1. Prepare the candidate branch

In your dd-trace-go checkout, ensure that the benchmark appears in a `BENCHMARKS`
list in `gitlab-ci.yml`. Check that its group uses `REPETITIONS: 10`.
Push the candidate branch and record its commit:

```bash
git push -u origin HEAD
git rev-parse HEAD
```

Keep the branch unchanged until the scan finishes. The workflow targets a branch,
so later samples could otherwise run a different commit. Leave existing quarantine
and performance thresholds unchanged during the scan.

## 2. Configure the stability workflow

In a clean checkout of
[`DataDog/benchmarking-platform-tools`](https://github.com/DataDog/benchmarking-platform-tools),
create a temporary branch:

```bash
git fetch origin main
git switch -c stability/dd-trace-go-check origin/main
```

Edit `scripts/stability/.gitlab/monitor-stability.yml`. Find `generate-dd-trace-go`
and add or override these entries in its `variables` block:

```yaml
    BP_STABILITY_TARGET_BRANCH: "your-candidate-branch"
    BP_STABILITY_N: "10"
    BP_STABILITY_WAVE_SIZES: "3,3,4"
    BP_STABILITY_BASELINE_OR_CANDIDATE: "candidate"
```

Replace `your-candidate-branch` with the dd-trace-go branch name. Keep the job's
existing `extends`, `rules`, and other variables.

These settings request ten candidate samples in three waves. Candidate-only
analysis prevents baseline measurements from entering the stability comparison.

From the benchmarking-platform-tools repository root, validate, commit, and push
the configuration:

```bash
make lint
git add scripts/stability/.gitlab/monitor-stability.yml
git commit -m "chore(stability): configure dd-trace-go candidate scan"
git push -u origin HEAD
```

Do not merge the temporary overrides into `main` or change the shared monitoring
branch. Those changes would redirect future scheduled scans to your candidate.

## 3. Start the scan in GitLab

1. Open the [benchmarking-platform-tools pipelines](https://gitlab.ddbuild.io/DataDog/benchmarking-platform-tools/-/pipelines).
2. Select the pipeline for the temporary configuration branch.
3. Open its **`stability` child pipeline**.
4. Play the manual **`generate-dd-trace-go`** job from the GitLab UI.

Commit the settings in the job configuration rather than supplying them as
play-time overrides. Play-time overrides do not reliably replace the generator
settings.

The generator uploads `stability-pipelines.generated.yml`. Confirm that the
artifact contains:

- Ten `trigger-sample-*` jobs targeting the candidate branch.
- Waves of three, three, and four samples.
- A distinct `BP_EXTERNAL_S3_URL` for each sample.
- Candidate-only analysis in `collect-dd-trace-go`.

Follow **`trigger-dd-trace-go`** into the generated child pipeline. Sample jobs
start downstream dd-trace-go pipelines. Barrier jobs wait for result uploads
before releasing the next wave. The scan can take several hours.

## 4. Verify the collected data

For each sample, open the downstream `microbenchmarks-N` job that contains the
benchmark. Check the job and its converted result files:

- All ten samples use the recorded candidate commit (`git_commit_sha`).
- All samples report the same CPU model (`cpu_model`) and Go version (`GO_VERSION`).
- Each sample contains the expected ten benchmark repetitions.
- The benchmark produces complete results in every sample.

Check the benchmark jobs and their metadata, not the generator's runner. The
generator and barriers do not measure benchmark performance.

A green parent pipeline does not prove completeness. Sample jobs and barriers
permit failures, and the collector can analyze fewer samples than requested.
Investigate missing data or barrier timeouts before accepting the scan.

## 5. Read the stability verdict

Open **`collect-dd-trace-go`** in the generated child pipeline. Download
`artifacts/summary.json`, `artifacts/summary.csv`, and `artifacts/summary.md`
from the job's artifacts.

Find the target benchmark and review execution time, allocated memory, and
allocations. For ten complete samples, each analyzed metric should have
`total_comparisons: 45` and no data warning. Review the `warning` field before
accepting a verdict.

The default analysis uses a **2% significant-impact threshold**. A metric is flaky
when more than **5%** of comparison pairs trigger significant-impact flags.
With 45 comparisons, at most two flags satisfy that limit. Prefer zero.
Use the significant-impact verdict rather than t-test significance alone.

After the benchmark passes, remove its quarantine. Keep the reporting and blocking
thresholds unchanged. Record the candidate commit, sample counts, hardware,
results, and collector link in the PR.

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

Before marking a benchmark as flaky, follow
[How to check benchmark stability](#how-to-check-benchmark-stability).

# How to rebuild the CI Docker image

The `MICROBENCHMARKS_CI_IMAGE` used by the microbenchmarks pipelines
is built from Dockerfile in `.gitlab/benchmarks/micro/container/`. You need to rebuild and update
the pinned tag whenever you change any of files in that directory (e.g. bumping the Go
base image, adding system packages, or updating `bp-runner` / `bp-analyzer` /
`bp-github` versions).

Steps:

1. Edit the relevant file(s) under `.gitlab/benchmarks/micro/container/`. Push
   the branch and open a PR.

2. The pipeline includes
   [`build-ci-images.template.yml`](https://github.com/DataDog/benchmarking-platform-tools/blob/main/images/templates/gitlab/build-ci-images.template.yml),
   which defines a `build-benchmark-ci-images` job (`when: manual`) that builds
   and pushes the image from `BASE_CI_IMAGE_CONTAINER_DIR`
   (`.gitlab/benchmarks/micro/container`) to the registry.

3. Open the pipeline on
   [`gitlab.ddbuild.io/DataDog/apm-reliability/dd-trace-go`](https://gitlab.ddbuild.io/DataDog/apm-reliability/dd-trace-go)
   and manually run the `build-benchmark-ci-images` job. Wait for it to finish
   successfully and copy the job URL and the resulting image tag from its logs.

4. Update both files to point at the newly built image (replace the job URL
   in the comment and the image tag):

   - `.gitlab/benchmarks/micro/gitlab-ci.yml`
   - `.gitlab/benchmarks/test-apps/test-apps.yml`

   In micro/gitlab-ci.yml the variable is named MICROBENCHMARKS_CI_IMAGE:
   ```yaml
   # Benchmarks image is created here: <paste build-benchmark-ci-images job URL>
   MICROBENCHMARKS_CI_IMAGE: registry.ddbuild.io/ci/benchmarking-platform:dd-trace-go-<NEW_PIPELINE_ID>
   ```
   
   In test-apps/test-apps.yml it is named TESTAPPS_BENCHMARKS_CI_IMAGE:
   ```yaml
   # Benchmarks image is created here: <paste build-benchmark-ci-images job URL>
   TESTAPPS_BENCHMARKS_CI_IMAGE: registry.ddbuild.io/ci/benchmarking-platform:dd-trace-go-<NEW_PIPELINE_ID>
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

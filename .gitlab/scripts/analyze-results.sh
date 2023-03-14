#!/usr/bin/env bash

# Change threshold for detection of regression
# @see https://github.com/DataDog/relenv-benchmark-analyzer#what-is-a-significant-difference
export UNCONFIDENCE_THRESHOLD=2.0
export FAIL_ON_REGRESSION_THRESHOLD=2.0

CANDIDATE_BRANCH=$CI_COMMIT_REF_NAME
CANDIDATE_SRC="/app/candidate/"

cd "$CANDIDATE_SRC"
CANDIDATE_COMMIT_SHA=$(git rev-parse --short HEAD)

benchmark_analyzer convert \
  --framework=GoBench \
  --extra-params="{\
    \"config\":\"candidate\", \
    \"git_commit_sha\":\"$CANDIDATE_COMMIT_SHA\", \
    \"git_branch\":\"$CANDIDATE_BRANCH\"\
    }" \
  --outpath="${ARTIFACTS_DIR}/pr.converted.json" \
  "${ARTIFACTS_DIR}/pr_bench.txt"

BASELINE_SRC="/app/baseline/"
if [ -d $BASELINE_SRC ]; then
  BASELINE_BRANCH=$(github-find-merge-into-branch --for-repo="$CI_PROJECT_NAME" --for-pr="$CANDIDATE_BRANCH" || :)

  cd "$BASELINE_SRC"
  BASELINE_COMMIT_SHA=$(git rev-parse --short HEAD)

  benchmark_analyzer convert \
    --framework=GoBench \
    --extra-params="{\
      \"config\":\"baseline\", \
      \"git_commit_sha\":\"$BASELINE_COMMIT_SHA\", \
      \"git_branch\":\"$BASELINE_BRANCH\"\
      }" \
    --outpath="${ARTIFACTS_DIR}/main.converted.json" \
    "${ARTIFACTS_DIR}/main_bench.txt"

  benchmark_analyzer compare pairwise \
    --baseline='{"config":"baseline"}' \
    --candidate='{"config":"candidate"}' \
    --outpath "${ARTIFACTS_DIR}/report_full.html" \
    --format html \
    "${ARTIFACTS_DIR}/main.converted.json" \
    "${ARTIFACTS_DIR}/pr.converted.json"

  if ! benchmark_analyzer compare pairwise \
    --baseline='{"config":"baseline"}' \
    --candidate='{"config":"candidate"}' \
    --outpath "${ARTIFACTS_DIR}/report.md" \
    --fail_on_regression=True \
    --format md-nodejs \
    "${ARTIFACTS_DIR}/main.converted.json" \
    "${ARTIFACTS_DIR}/pr.converted.json"; then
      mkdir -p "${ARTIFACTS_DIR}/candidate-profile"
      cd "$CANDIDATE_SRC/ddtrace/tracer/"
      go test -run=XXX -bench "BenchmarkConcurrentTracing|BenchmarkStartSpan" \
        -cpuprofile "${ARTIFACTS_DIR}/candidate-profile/cpu.pprof" \
        -memprofile "${ARTIFACTS_DIR}/candidate-profile/mem.pprof" \
        -benchmem -count 10 -benchtime 2s ./...

      mkdir -p "${ARTIFACTS_DIR}/baseline-profile"
      cd "$BASELINE_SRC/ddtrace/tracer/"
      go test -run=XXX -bench "BenchmarkConcurrentTracing|BenchmarkStartSpan" \
        -cpuprofile "${ARTIFACTS_DIR}/baseline-profile/cpu.pprof" \
        -memprofile "${ARTIFACTS_DIR}/baseline-profile/mem.pprof" \
        -benchmem -count 10 -benchtime 2s ./...

      # TODO: Upload profiles to Datadog
  fi
else
  benchmark_analyzer analyze --outpath "${ARTIFACTS_DIR}/analysis.html" --format html "${ARTIFACTS_DIR}/pr.converted.json"
fi

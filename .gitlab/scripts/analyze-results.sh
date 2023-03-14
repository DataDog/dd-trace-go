#!/usr/bin/env bash

set -x

export UNSTABLE_CI_WIDTH=10000.0

source ./.gitlab/scripts/config-benchmarks.sh
INITIAL_DIR=$(pwd)

CANDIDATE_BRANCH=$CI_COMMIT_REF_NAME

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
      "$INITIAL_DIR/.gitlab/scripts/run-benchmarks-with-profiler.sh"
  fi
else
  benchmark_analyzer analyze --outpath "${ARTIFACTS_DIR}/analysis.html" --format html "${ARTIFACTS_DIR}/pr.converted.json"
fi

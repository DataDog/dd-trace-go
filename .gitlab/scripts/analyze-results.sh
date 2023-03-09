#!/usr/bin/env bash

# Change threshold for detection of regression
# @see https://github.com/DataDog/relenv-benchmark-analyzer#what-is-a-significant-difference
export UNCONFIDENCE_THRESHOLD=2.0

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

  benchmark_analyzer compare pairwise --baseline='{"config":"baseline"}' --candidate='{"config":"candidate"}' --outpath "${ARTIFACTS_DIR}/report.md" --format md-nodejs "${ARTIFACTS_DIR}/main.converted.json" "${ARTIFACTS_DIR}/pr.converted.json"
  benchmark_analyzer compare pairwise --baseline='{"config":"baseline"}' --candidate='{"config":"candidate"}' --outpath "${ARTIFACTS_DIR}/report_full.html" --format html "${ARTIFACTS_DIR}/main.converted.json" "${ARTIFACTS_DIR}/pr.converted.json"
fi

#!/usr/bin/env bash

ARTIFACTS_DIR="/artifacts/${CI_JOB_ID}"
REPORTS_DIR="$(pwd)/reports/"
mkdir "${REPORTS_DIR}" || :

# Change threshold for detection of regression
# @see https://github.com/DataDog/relenv-benchmark-analyzer#what-is-a-significant-difference
export UNCONFIDENCE_THRESHOLD=2.0

CANDIDATE_COMMIT_SHA=$CI_COMMIT_SHA
CANDIDATE_BRANCH=$CI_COMMIT_REF_NAME

cd dd-trace-go/ddtrace/tracer/
BASELINE_COMMIT_SHA=$(git rev-parse HEAD)
BASELINE_BRANCH=$(github-find-merge-into-branch --for-repo="$CI_PROJECT_NAME" --for-pr="$CANDIDATE_BRANCH" || :)

source /benchmark-analyzer/.venv/bin/activate
cd /benchmark-analyzer

./benchmark_analyzer convert \
  --framework=GoBench \
  --extra-params="{\
    \"config\":\"candidate\", \
    \"git_commit_sha\":\"$CANDIDATE_COMMIT_SHA\", \
    \"git_branch\":\"$CANDIDATE_BRANCH\"\
    }" \
  --outpath="pr.json" \
  "${ARTIFACTS_DIR}/pr_bench.txt"

./benchmark_analyzer convert \
  --framework=GoBench \
  --extra-params="{\
    \"config\":\"baseline\", \
    \"git_commit_sha\":\"$BASELINE_COMMIT_SHA\", \
    \"git_branch\":\"$BASELINE_BRANCH\"\
    }" \
  --outpath="main.json" \
  "${ARTIFACTS_DIR}/main_bench.txt"

./benchmark_analyzer compare pairwise --baseline='{"config":"baseline"}' --candidate='{"config":"candidate"}' --outpath ${REPORTS_DIR}/report.md --format md-nodejs main.json pr.json
./benchmark_analyzer compare pairwise --baseline='{"config":"baseline"}' --candidate='{"config":"candidate"}' --outpath ${REPORTS_DIR}/report_full.html --format html main.json pr.json


#!/usr/bin/env bash

source ./.gitlab/scripts/config-benchmarks.sh
INITIAL_DIR=$(pwd)

CANDIDATE_BRANCH=main

cd "$CANDIDATE_SRC"
CANDIDATE_COMMIT_SHA=$(git rev-parse --short HEAD)

for CANDIDATE_COMMIT_SHA in ${BACKFILL_SHAS[@]}; do
  benchmark_analyzer convert \
    --framework=GoBench \
    --extra-params="{\
      \"config\":\"candidate\", \
      \"git_commit_sha\":\"${CANDIDATE_COMMIT_SHA:0:6}\", \
      \"git_branch\":\"$CANDIDATE_BRANCH\"\
      }" \
    --outpath="${ARTIFACTS_DIR}/pr_${CANDIDATE_COMMIT_SHA}.converted.json" \
    "${ARTIFACTS_DIR}/pr_bench_${CANDIDATE_COMMIT_SHA}.txt"
done
#!/usr/bin/env bash

set -ex

source ./.gitlab/scripts/config-benchmarks.sh

bench_loop_x10 () {
  for i in {1..10}
    do
      go test -run=XXX -bench $BENCHMARK_TARGETS -benchmem -count 1 -benchtime 2s ./... | tee -a $1
    done
}

CANDIDATE_BRANCH=main

for CANDIDATE_COMMIT_SHA in ${BACKFILL_SHAS[@]}; do
  # Clone candidate release
  git clone --branch "$CANDIDATE_BRANCH" https://github.com/DataDog/dd-trace-go "$CANDIDATE_SRC/$CANDIDATE_COMMIT_SHA" && \
    cd "$CANDIDATE_SRC/$CANDIDATE_COMMIT_SHA" && \
    git checkout $CANDIDATE_COMMIT_SHA

  # Run benchmarks for candidate release
  cd "$CANDIDATE_SRC/$CANDIDATE_COMMIT_SHA/ddtrace/tracer/"
  bench_loop_x10 "${ARTIFACTS_DIR}/pr_bench_${CANDIDATE_COMMIT_SHA}.txt"
done

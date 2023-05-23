#!/usr/bin/env bash

set -ex

source ./.gitlab/scripts/config-benchmarks.sh

bench_loop_x10 () {
  for i in {1..10}
    do
      go test -run=XXX -bench $BENCHMARK_TARGETS -benchmem -count 1 -benchtime 2s ./... | tee -a $1
    done
}

CANDIDATE_BRANCH=$CI_COMMIT_REF_NAME
CANDIDATE_COMMIT_SHA=$CI_COMMIT_SHA

# Clone candidate release
git clone --branch "$CANDIDATE_BRANCH" https://github.com/DataDog/dd-trace-go "$CANDIDATE_SRC" && \
  cd "$CANDIDATE_SRC" && \
  git checkout $CANDIDATE_COMMIT_SHA

# Run benchmarks for candidate release
cd "$CANDIDATE_SRC/ddtrace/"
bench_loop_x10 "${ARTIFACTS_DIR}/pr_bench.txt"

BASELINE_BRANCH=$(github-find-merge-into-branch --for-repo="$CI_PROJECT_NAME" --for-pr="$CANDIDATE_BRANCH" || :)

if [ ! -z "$BASELINE_BRANCH" ]; then
  cd "$CANDIDATE_SRC"
  BASELINE_COMMIT_SHA=$(git merge-base "origin/$BASELINE_BRANCH" "origin/$CANDIDATE_BRANCH")

  # Clone baseline release
  git clone --branch "$BASELINE_BRANCH" https://github.com/DataDog/dd-trace-go/ "$BASELINE_SRC" && \
    cd "$BASELINE_SRC" && \
    git checkout $BASELINE_COMMIT_SHA

  # Run benchmarks for baseline release
  cd "$BASELINE_SRC/ddtrace/"
  bench_loop_x10 "${ARTIFACTS_DIR}/main_bench.txt"
fi

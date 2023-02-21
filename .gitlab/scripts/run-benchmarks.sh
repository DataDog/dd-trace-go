#!/usr/bin/env bash

set -x

ARTIFACTS_DIR="/artifacts/${CI_JOB_ID}"
mkdir -p "${ARTIFACTS_DIR}"

git clone --branch "${CI_COMMIT_REF_NAME}" https://github.com/DataDog/dd-trace-go

cd dd-trace-go/ddtrace/tracer/
go test -run=XXX -bench "BenchmarkConcurrentTracing|BenchmarkStartSpan" -benchmem -count 10 -benchtime 2s ./... | tee ${ARTIFACTS_DIR}/pr_bench.txt

CANDIDATE_BRANCH="$CI_COMMIT_REF_NAME"
BASELINE_BRANCH=$(github-find-merge-into-branch --for-repo="$CI_PROJECT_NAME" --for-pr="$CANDIDATE_BRANCH" || :)
BASELINE_COMMIT_SHA=$(git merge-base "origin/$BASELINE_BRANCH" "origin/$CANDIDATE_BRANCH")

git checkout "$BASELINE_COMMIT_SHA"
go test -run=XXX -bench "BenchmarkConcurrentTracing|BenchmarkStartSpan" -benchmem -count 10 -benchtime 2s ./... | tee ${ARTIFACTS_DIR}/main_bench.txt

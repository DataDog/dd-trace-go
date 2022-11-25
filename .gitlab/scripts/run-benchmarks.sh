#!/usr/bin/env bash

ARTIFACTS_DIR="/artifacts/${CI_JOB_ID}"
mkdir -p "${ARTIFACTS_DIR}"

git clone --branch ${CI_COMMIT_REF_NAME} https://github.com/DataDog/dd-trace-go

cd dd-trace-go/ddtrace/tracer/
go test -run=XXX -bench "BenchmarkConcurrentTracing|BenchmarkStartSpan" -benchmem -count 10 -benchtime 2s ./... | tee ${ARTIFACTS_DIR}/pr_bench.txt

git checkout main
go test -run=XXX -bench "BenchmarkConcurrentTracing|BenchmarkStartSpan" -benchmem -count 10 -benchtime 2s ./... | tee ${ARTIFACTS_DIR}/main_bench.txt

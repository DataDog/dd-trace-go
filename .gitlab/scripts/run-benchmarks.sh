#!/usr/bin/env bash

ARTIFACTS_DIR="/artifacts/${CI_JOB_ID}"
mkdir -p "${ARTIFACTS_DIR}"

# echo "Insert code for running your benchmarks here. The output of benchmarks must be saved into /artifacts/\${CI_JOB_ID} directory."

git clone --branch ${CI_COMMIT_REF_NAME} https://github.com/DataDog/dd-trace-go

cd dd-trace-go/ddtrace/tracer/
go test -run=XXX -bench "BenchmarkConcurrentTracing|BenchmarkStartSpan" -benchmem -count 10 -benchtime 5s ./... | tee ${ARTIFACTS_DIR}/pr_bench.txt


git checkout main
go test -run=XXX -bench "BenchmarkConcurrentTracing|BenchmarkStartSpan" -benchmem -count 10 -benchtime 5s ./... | tee ${ARTIFACTS_DIR}/main_bench.txt
# 
# 
# ./benchmark_analyzer compare pairwise --outpath "ahhh.md" --format md-nodejs 1_35_0.json 1_43_1Rep.json
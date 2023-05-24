#!/usr/bin/env bash

export CANDIDATE_SRC="/app/candidate/"
export BASELINE_SRC="/app/baseline/"

# Change threshold for detection of regression
# @see https://github.com/DataDog/relenv-benchmark-analyzer#what-is-a-significant-difference
export UNCONFIDENCE_THRESHOLD=2.0
export FAIL_ON_REGRESSION_THRESHOLD=$UNCONFIDENCE_THRESHOLD

export BENCHMARK_TARGETS="BenchmarkConcurrentTracing|BenchmarkStartSpan|BenchmarkSingleSpanRetention|BenchmarkOTelApiWithCustomTags|BenchmarkInjectW3C"

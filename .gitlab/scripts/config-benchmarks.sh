#!/usr/bin/env bash

export CANDIDATE_SRC="/app/candidate/"
export BASELINE_SRC="/app/baseline/"

# Change threshold for detection of regression
# @see https://github.com/DataDog/relenv-benchmark-analyzer#what-is-a-significant-difference
export UNCONFIDENCE_THRESHOLD=2.0
export FAIL_ON_REGRESSION_THRESHOLD=$UNCONFIDENCE_THRESHOLD

export BENCHMARK_TARGETS="BenchmarkConcurrentTracing|BenchmarkStartSpan|BenchmarkSingleSpanRetention|BenchmarkInjectW3C"

export BACKFILL_SHAS=("4d0df867ef26d03d859fdc84ceb6e828ae434eee" "357ceedac2db830068213e1a0498cb6384e21e9e" "3c565f90384d25c732afab25b4235eba0994c79f" "1e85afab8f4c7231328fe37358c5d78d7cfa4b59" "94ee98b2948c4acb5b2dc525132edf4b872ccb20" "b66877c789822e1486d43c59ee856e7689e87e7b" "f56bd6f30dbc343350533a6ef653c8734ae7f376" "9ca91f368e5aa83ce972b1afd9cbc84f387feb53" "4e267b44c20051135d9400cdee3961245bb24f6a" "9b600bf3e48275a524e13dab5718da297d1a0e89" "bcb15de0ff956d97a156d5aa080aaa43843ad6d8" "a09c872725b0e347b555e99d90a0163dc5339684" "1c6fa2ecf36adf847170cb2e62d2945f9a0d79aa" "f0ad6d68074795945def79be48da3851251c0e8f" "743f74fdaf41ffb0a8d6f43f63e4d06cf37b87e4" "e559ab737b0a76d65fba3744d2911dc8a6276bcb" "69b9d79254c614058ad9635ecdcdda7325f73f3e")
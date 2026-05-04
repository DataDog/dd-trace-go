#!/usr/bin/env bash

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/config-benchmarks.sh"

if [ -f "${ARTIFACTS_DIR}/baseline.converted.json" ]; then
  benchmark_analyzer compare pairwise \
    --baseline='{"baseline_or_candidate":"baseline"}' \
    --candidate='{"baseline_or_candidate":"candidate"}' \
    --outpath "${ARTIFACTS_DIR}/report_full.html" \
    --format html \
    "${ARTIFACTS_DIR}/baseline.converted.json" \
    "${ARTIFACTS_DIR}/candidate.converted.json"

  if ! benchmark_analyzer compare pairwise \
    --baseline='{"baseline_or_candidate":"baseline"}' \
    --candidate='{"baseline_or_candidate":"candidate"}' \
    --outpath "${ARTIFACTS_DIR}/report.md" \
    --fail_on_regression=True \
    --format md-nodejs \
    "${ARTIFACTS_DIR}/baseline.converted.json" \
    "${ARTIFACTS_DIR}/candidate.converted.json"; then
      "${SCRIPT_DIR}/run-benchmarks-with-profiler.sh"
  fi
else
  benchmark_analyzer analyze --outpath "${ARTIFACTS_DIR}/analysis.html" --format html "${ARTIFACTS_DIR}/candidate.converted.json"
fi

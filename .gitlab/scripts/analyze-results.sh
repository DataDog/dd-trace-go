#!/usr/bin/env bash

ARTIFACTS_DIR="/artifacts/${CI_JOB_ID}"
REPORTS_DIR="$(pwd)/reports/"
mkdir "${REPORTS_DIR}" || :

BASELINE_BRANCH=$(github-find-merge-into-branch --for-repo="$CI_PROJECT_NAME" --for-pr="$CI_COMMIT_REF_NAME" || :)

source /benchmark-analyzer/.venv/bin/activate
cd /benchmark-analyzer

./benchmark_analyzer convert \
  --framework=GoBench \
  --outpath="pr.json" \
  --extra-params="{\"dd_trace_go\":\"${CI_COMMIT_REF_NAME}\"}" \
  "${ARTIFACTS_DIR}/pr_bench.txt"

./benchmark_analyzer convert \
  --framework=GoBench \
  --outpath="main.json" \
  --extra-params="{\"dd_trace_go\":\"main\"}" \
  "${ARTIFACTS_DIR}/main_bench.txt"

./benchmark_analyzer compare pairwise --outpath ${REPORTS_DIR}/report.md --format md-nodejs main.json pr.json
./benchmark_analyzer compare pairwise --outpath ${REPORTS_DIR}/report_full.html --format html main.json pr.json

# Extra artifacts for S3 uploader job
cp "${ARTIFACTS_DIR}/pr_bench.txt" "$REPORTS_DIR/pr_bench.txt"
cp "${ARTIFACTS_DIR}/main_bench.txt" "$REPORTS_DIR/main_bench.txt"
cp "/benchmark-analyzer/pr.json" "$REPORTS_DIR/pr_bench.json"
cp "/benchmark-analyzer/main.json" "$REPORTS_DIR/main_bench.json"
echo "UPSTREAM_JOB_ID=$CI_JOB_ID" >> "$REPORTS_DIR/benchmark.env"

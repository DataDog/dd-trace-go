#!/bin/bash
set -euo pipefail

echo "-> Starting benchmark (ETA: 90min)"
BENCH_CMD="go test -benchtime 60s -count 5 -timeout 24h -run ^$ -bench . ."

if ! which benchstat; then
  echo "error: needs benchstat, install via:"
  echo "make tools-install"
  exit 1
fi

$BENCH_CMD | tee baseline.txt

env \
  BENCH_HOTSPOTS=true \
  BENCH_ENDPOINTS=true \
  "${BENCH_CMD}" | tee endpoints-and-hotspots.txt

benchstat -sort delta baseline.txt endpoints-and-hotspots.txt | tee overhead.txt

set -eu
echo "-> Starting benchmark (ETA: 90min)"
BENCH_CMD="go test -benchtime 60t as -count 5 -timeout 24h -run ^$ -bench . ."

if ! which benchstat; then
  echo "error: needs benchstat, install via:"
  echo "go install golang.org/x/perf/cmd/benchstat@latest"
  exit 1
fi

$BENCH_CMD | tee baseline.txt

env \
  BENCH_HOTSPOTS=true \
  BENCH_ENDPOINTS=true \
  $BENCH_CMD | tee endpoints-and-hotspots.txt

benchstat baseline.txt endpoints-and-hotspots.txt | tee overhead.txt

#!/usr/bin/env bash

# Add packages with location of your benchmarks into this variable
BENCHMARK_PACKAGES=(
  "ddtrace/tracer"
  "contrib/net/http"
)

V2_BENCHMARK_PACKAGES=(
  "ddtrace/tracer"
  "contrib/net/http"
)

DD_API_KEY=$(aws ssm get-parameter --region us-east-1 --name ci.dd-trace-go.dd_api_key --with-decryption --query "Parameter.Value" --out text)

set -e

INTAKE_API_HOST=intake.profile.datad0g.com

# Run candidate and baseline the same way like in run-benchmarks.sh, but with profiler enabled
bench_loop_x5() {
  if [ -d 'v2' ]; then
    bench_loop_x5_v2 $1
    return
  fi

  VARIANT=$1
  ORIG_DIR=$(pwd)

  mkdir -p "${ARTIFACTS_DIR}/$VARIANT-profile"
  for i in {1..5}; do
    for BP_GO_MODULE in "${BENCHMARK_PACKAGES[@]}"; do
      BP_GO_MODULE_NORMALIZED=$(echo "$BP_GO_MODULE" | sed "s#/#-#g")

      START_DATE=$(date -u --iso-8601=seconds | sed 's/\+.*/Z/')

      cd "$ORIG_DIR/$BP_GO_MODULE"
      set -ex

      # Subpackages are not scanned for benchmarks, because `go test` fails with an error
      # `go cannot use -cpuprofile flag with multiple packages` if ./... is specified.
      go test -run=XXX -bench $BENCHMARK_TARGETS \
        -cpuprofile "${ARTIFACTS_DIR}/$VARIANT-profile/cpu-$BP_GO_MODULE_NORMALIZED-$i.pprof" \
        -memprofile "${ARTIFACTS_DIR}/$VARIANT-profile/delta-heap-$BP_GO_MODULE_NORMALIZED-$i.pprof" \
        -benchmem -count 1 -benchtime 2s ./

      set -e +x

      END_DATE=$(date -u --iso-8601=seconds | sed 's/\+.*/Z/')

      echo "Sending profiles $VARIANT, $BP_GO_MODULE_NORMALIZED, #$i to Datadog backend..."

      cd "${ARTIFACTS_DIR}/$VARIANT-profile"
      curl -i  \
        -H "DD-API-KEY: $DD_API_KEY" \
        -H "DD-EVP-ORIGIN: test-origin"\
        -H "DD-EVP-ORIGIN-VERSION: test-origin-version"\
        -F "delta-heap.pprof=@delta-heap-$BP_GO_MODULE_NORMALIZED-$i.pprof" \
        -F "cpu.pprof=@cpu-$BP_GO_MODULE_NORMALIZED-$i.pprof" \
        -F "event=@-;type=application/json" \
        https://$INTAKE_API_HOST/api/v2/profile <<END
{
 "attachments":[ "cpu.pprof", "delta-heap.pprof" ],
 "tags_profiler":"service:dd-trace-go-benchmarks,job_id:$CI_JOB_ID,config:$VARIANT",
 "start":"$START_DATE",
 "end":"$END_DATE",
 "family":"go",
 "version":"4"
}
END
    done
  done
}

# Run v2 candidate and baseline the same way like in run-benchmarks.sh, but with profiler enabled
bench_loop_x5_v2() {
  VARIANT=$1
  ORIG_DIR=$(pwd)

  mkdir -p "${ARTIFACTS_DIR}/$VARIANT-profile"
  for i in {1..5}; do
    for BP_GO_MODULE in "${V2_BENCHMARK_PACKAGES[@]}"; do
      BP_GO_MODULE_NORMALIZED=$(echo "$BP_GO_MODULE" | sed "s#/#-#g")

      START_DATE=$(date -u --iso-8601=seconds | sed 's/\+.*/Z/')

      cd "$ORIG_DIR/$BP_GO_MODULE"
      set -ex

      # Subpackages are not scanned for benchmarks, because `go test` fails with an error
      # `go cannot use -cpuprofile flag with multiple packages` if ./... is specified.
      go test -run=XXX -bench $BENCHMARK_TARGETS \
        -cpuprofile "${ARTIFACTS_DIR}/$VARIANT-profile/cpu-$BP_GO_MODULE_NORMALIZED-$i.pprof" \
        -memprofile "${ARTIFACTS_DIR}/$VARIANT-profile/delta-heap-$BP_GO_MODULE_NORMALIZED-$i.pprof" \
        -benchmem -count 1 -benchtime 2s ./

      set -e +x

      END_DATE=$(date -u --iso-8601=seconds | sed 's/\+.*/Z/')

      echo "Sending profiles $VARIANT, $BP_GO_MODULE_NORMALIZED, #$i to Datadog backend..."

      cd "${ARTIFACTS_DIR}/$VARIANT-profile"
      curl -i  \
        -H "DD-API-KEY: $DD_API_KEY" \
        -H "DD-EVP-ORIGIN: test-origin"\
        -H "DD-EVP-ORIGIN-VERSION: test-origin-version"\
        -F "delta-heap.pprof=@delta-heap-$BP_GO_MODULE_NORMALIZED-$i.pprof" \
        -F "cpu.pprof=@cpu-$BP_GO_MODULE_NORMALIZED-$i.pprof" \
        -F "event=@-;type=application/json" \
        https://$INTAKE_API_HOST/api/v2/profile <<END
{
 "attachments":[ "cpu.pprof", "delta-heap.pprof" ],
 "tags_profiler":"service:dd-trace-go-benchmarks,job_id:$CI_JOB_ID,config:$VARIANT",
 "start":"$START_DATE",
 "end":"$END_DATE",
 "family":"go",
 "version":"4"
}
END
    done
  done
}

PROFILER_RUNS_START_DATE=$(date -u --iso-8601=minutes | sed 's/\+.*/Z/')

cd "$CANDIDATE_SRC" && bench_loop_x5 "candidate"
cd "$BASELINE_SRC"  && bench_loop_x5 "baseline"

PROFILER_RUNS_END_DATE=$(date -d '+1 min' -u --iso-8601=minutes | sed 's/\+.*/Z/')

echo ""
echo ""
echo "Profiles were uploaded to Datadog! Open the following link to see them:"
echo ""
echo "https://dd.datad0g.com/profiling/comparison?query=service%3Add-trace-go-benchmarks&compare_end_A=$(date -d"$PROFILER_RUNS_END_DATE" +%s)000&compare_end_B=$(date -d"$PROFILER_RUNS_END_DATE" +%s)000&compare_query_B=service%3Add-trace-go-benchmarks%20config%3Acandidate%20%20job_id%3A${CI_JOB_ID}&compare_query_A=service%3Add-trace-go-benchmarks%20config%3Abaseline%20job_id%3A${CI_JOB_ID}&compare_start_A=$(date -d"$PROFILER_RUNS_START_DATE" +%s)000&compare_start_B=$(date -d"$PROFILER_RUNS_START_DATE" +%s)000&compareValuesMode=absolute&my_code=enabled&viz=flame_graph&paused=true"
echo ""
echo ""

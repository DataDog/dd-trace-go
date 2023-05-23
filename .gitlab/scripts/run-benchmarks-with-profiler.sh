#!/usr/bin/env bash

set -e

DD_API_KEY=$(aws ssm get-parameter --region us-east-1 --name ci.dd-trace-go.dd_api_key --with-decryption --query "Parameter.Value" --out text)
INTAKE_API_HOST=intake.profile.datad0g.com

PROFILER_RUNS_START_DATE=$(date -u --iso-8601=minutes | sed 's/\+.*/Z/')

# Run candidate and baseline the same way like in run-benchmarks.sh, but with profiler enabled
mkdir -p "${ARTIFACTS_DIR}/candidate-profile"
cd "$CANDIDATE_SRC/ddtrace/"
CANDIDATE_START_DATE=$(date -u --iso-8601=seconds | sed 's/\+.*/Z/')
go test -run=XXX -bench $BENCHMARK_TARGETS \
  -cpuprofile "${ARTIFACTS_DIR}/candidate-profile/cpu.pprof" \
  -memprofile "${ARTIFACTS_DIR}/candidate-profile/delta-heap.pprof" \
  -benchmem -count 10 -benchtime 2s ./...
CANDIDATE_END_DATE=$(date -u --iso-8601=seconds | sed 's/\+.*/Z/')

mkdir -p "${ARTIFACTS_DIR}/baseline-profile"
cd "$BASELINE_SRC/ddtrace/"
BASELINE_START_DATE=$(date -u --iso-8601=seconds | sed 's/\+.*/Z/')
go test -run=XXX -bench $BENCHMARK_TARGETS \
  -cpuprofile "${ARTIFACTS_DIR}/baseline-profile/cpu.pprof" \
  -memprofile "${ARTIFACTS_DIR}/baseline-profile/delta-heap.pprof" \
  -benchmem -count 10 -benchtime 2s ./...
BASELINE_END_DATE=$(date -u --iso-8601=seconds | sed 's/\+.*/Z/')

PROFILER_RUNS_END_DATE=$(date -d '+1 min' -u --iso-8601=minutes | sed 's/\+.*/Z/')

# Upload profiles to Datadog
cd "${ARTIFACTS_DIR}/baseline-profile"
curl -i  \
  -H "DD-API-KEY: $DD_API_KEY" \
  -H "DD-EVP-ORIGIN: test-origin"\
  -H "DD-EVP-ORIGIN-VERSION: test-origin-version"\
  -F "delta-heap.pprof=@delta-heap.pprof" \
  -F "cpu.pprof=@cpu.pprof" \
  -F "event=@-;type=application/json" \
  https://$INTAKE_API_HOST/api/v2/profile <<END
{
 "attachments":[ "cpu.pprof", "delta-heap.pprof" ],
 "tags_profiler":"service:dd-trace-go-benchmarks,job_id:$CI_JOB_ID,config:baseline",
 "start":"$BASELINE_START_DATE",
 "end":"$BASELINE_END_DATE",
 "family":"go",
 "version":"4"
}
END

cd "${ARTIFACTS_DIR}/candidate-profile"
curl -i  \
  -H "DD-API-KEY: $DD_API_KEY" \
  -H "DD-EVP-ORIGIN: test-origin"\
  -H "DD-EVP-ORIGIN-VERSION: test-origin-version"\
  -F "delta-heap.pprof=@delta-heap.pprof" \
  -F "cpu.pprof=@cpu.pprof" \
  -F "event=@-;type=application/json" \
  https://$INTAKE_API_HOST/api/v2/profile <<END
{
 "attachments":[ "cpu.pprof", "delta-heap.pprof" ],
 "tags_profiler":"service:dd-trace-go-benchmarks,job_id:$CI_JOB_ID,config:candidate",
 "start":"$CANDIDATE_START_DATE",
 "end":"$CANDIDATE_END_DATE",
 "family":"go",
 "version":"4"
}
END

echo ""
echo ""
echo "Profiles were uploaded to Datadog! Open the following link to see them:"
echo ""
echo "https://dd.datad0g.com/profiling/comparison?query=service%3Add-trace-go-benchmarks&compare_end_A=$(date -d"$PROFILER_RUNS_END_DATE" +%s)000&compare_end_B=$(date -d"$PROFILER_RUNS_END_DATE" +%s)000&compare_query_B=service%3Add-trace-go-benchmarks%20config%3Acandidate%20%20job_id%3A${CI_JOB_ID}&compare_query_A=service%3Add-trace-go-benchmarks%20config%3Abaseline%20job_id%3A${CI_JOB_ID}&compare_start_A=$(date -d"$PROFILER_RUNS_START_DATE" +%s)000&compare_start_B=$(date -d"$PROFILER_RUNS_START_DATE" +%s)000&compareValuesMode=absolute&my_code=enabled&viz=flame_graph&paused=true"
echo ""
echo ""
#!/usr/bin/env bash

set -eu

report_error=0
BUILD_TAGS="${BUILD_TAGS:-}"

TEST_RESULTS="${TEST_RESULTS:-.}"
mkdir -p "$TEST_RESULTS"

GO_CMD="${GO_CMD:-go}"
echo "Running tests with $GO_CMD"

# Packages that don't support -shuffle on yet
NO_SHUFFLE_PATTERN="(github\.com/DataDog/dd-trace-go/v2/ddtrace/tracer|\
github\.com/DataDog/dd-trace-go/v2/internal/civisibility/utils|\
github\.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo|\
github\.com/DataDog/dd-trace-go/v2/instrumentation/httptrace)$"

mapfile -t SHUFFLE_PACKAGES < <($GO_CMD list ./... | grep -v /contrib/ | grep -Ev "$NO_SHUFFLE_PATTERN")
mapfile -t NO_SHUFFLE_PACKAGES < <($GO_CMD list ./... | grep -v /contrib/ | grep -E "$NO_SHUFFLE_PATTERN")

# Set +e so that we run all test commands even if one fails
set +e

# Opt-in flaky-failure retry; only the dynamic-analysis workflow sets it. A
# rerun overwrites -coverprofile with its partial profile, so any caller that
# uploads coverage must leave RERUN_FAILS unset. gotestsum still exits non-zero
# when a test fails every attempt, and keeps each one in the JUnit XML.
# Only "" and "0" mean off; any other value is passed through verbatim, so a
# boolean-ish "true" fails gotestsum at flag-parse time rather than disabling
# retries.
# ${RERUN_ARGS[@]+...} guards bash 3.2, which treats "${ARR[@]}" on an empty
# array as unbound under `set -u` and aborts.
RERUN_ARGS=()
if [[ -n "${RERUN_FAILS:-}" && "${RERUN_FAILS}" != "0" ]]; then
  RERUN_ARGS=(--rerun-fails="${RERUN_FAILS}")
  echo "Retrying failed tests up to ${RERUN_FAILS} time(s); coverage from a rerun is not trustworthy"
fi

# Build the tags argument if BUILD_TAGS is set
TAGS_ARG="-tags="
if [[ -n "$BUILD_TAGS" ]]; then
  TAGS_ARG="-tags=$BUILD_TAGS"
  echo "Running tests for core packages with build tags: $BUILD_TAGS"
else
  echo "Running standard tests for core packages"
fi

# Run tests with shuffle for packages that support it
gotestsum --raw-command ${RERUN_ARGS[@]+"${RERUN_ARGS[@]}"} --junitfile "${TEST_RESULTS}/gotestsum-report.xml" -- "$GO_CMD" test -json -v -race "$TAGS_ARG" -shuffle=on -coverprofile=coverage.txt -covermode=atomic "${SHUFFLE_PACKAGES[@]}"
test_exit=$?
[[ $test_exit -ne 0 ]] && report_error=1

# Run tests without shuffle for packages that don't support it yet
gotestsum --raw-command ${RERUN_ARGS[@]+"${RERUN_ARGS[@]}"} --junitfile "${TEST_RESULTS}/gotestsum-report-noshuffle.xml" -- "$GO_CMD" test -json -v -race "$TAGS_ARG" -coverprofile=coverage-noshuffle.txt -covermode=atomic "${NO_SHUFFLE_PACKAGES[@]}"
test_exit=$?
[[ $test_exit -ne 0 ]] && report_error=1

cd ./internal/exectracetest
gotestsum --raw-command ${RERUN_ARGS[@]+"${RERUN_ARGS[@]}"} --junitfile "${TEST_RESULTS}/gotestsum-report-exectrace.xml" -- "$GO_CMD" test -json -v -race "$TAGS_ARG" -coverprofile=coverage.txt -covermode=atomic ./...
test_exit=$?
[[ $test_exit -ne 0 ]] && report_error=1

exit $report_error

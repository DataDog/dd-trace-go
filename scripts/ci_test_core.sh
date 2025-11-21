#!/bin/bash

set -eu

report_error=0

TEST_RESULTS="${TEST_RESULTS:-.}"
mkdir -p "$TEST_RESULTS"

# Packages that don't support -shuffle on yet
NO_SHUFFLE_PATTERN="(github\.com/DataDog/dd-trace-go/v2/ddtrace/tracer|\
github\.com/DataDog/dd-trace-go/v2/internal/civisibility/utils|\
github\.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo|\
github\.com/DataDog/dd-trace-go/v2/instrumentation/httptrace)$"

mapfile -t SHUFFLE_PACKAGES < <(go list ./... | grep -v /contrib/ | grep -Ev "$NO_SHUFFLE_PATTERN")
mapfile -t NO_SHUFFLE_PACKAGES < <(go list ./... | grep -v /contrib/ | grep -E "$NO_SHUFFLE_PATTERN")

# Set +e so that we run all test commands even if one fails
set +e

export GOEXPERIMENT=synctest # TODO: remove once go1.25 is the minimum supported version

# Run tests with shuffle for packages that support it
gotestsum --junitfile "${TEST_RESULTS}/gotestsum-report.xml" -- -v -race -shuffle=on -coverprofile=coverage.txt -covermode=atomic "${SHUFFLE_PACKAGES[@]}"
test_exit=$?
[[ $test_exit -ne 0 ]] && report_error=1

# Run tests without shuffle for packages that don't support it yet
gotestsum --junitfile "${TEST_RESULTS}/gotestsum-report-noshuffle.xml" -- -v -race -coverprofile=coverage-noshuffle.txt -covermode=atomic "${NO_SHUFFLE_PACKAGES[@]}"
test_exit=$?
[[ $test_exit -ne 0 ]] && report_error=1

cd ./internal/exectracetest
gotestsum --junitfile "${TEST_RESULTS}/gotestsum-report-exectrace.xml" -- -v -race -coverprofile=coverage.txt -covermode=atomic ./...
test_exit=$?
[[ $test_exit -ne 0 ]] && report_error=1

exit $report_error

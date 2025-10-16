#!/bin/bash

set -eu

report_error=0

TEST_RESULTS="${TEST_RESULTS:-.}"
mkdir -p "$TEST_RESULTS"
mapfile -t PACKAGE_NAMES < <(go list ./... | grep -v /contrib/)

# Set +e so that we run both of the test commands even if
# the first one fails
set +e

export GOEXPERIMENT=synctest # TODO: remove once go1.25 is the minimum supported version

gotestsum --junitfile "${TEST_RESULTS}/gotestsum-report.xml" -- -v -race -coverprofile=coverage.txt -covermode=atomic "${PACKAGE_NAMES[@]}"
test_exit=$?
[[ $test_exit -ne 0 ]] && report_error=1
cd ./internal/exectracetest
gotestsum --junitfile "${TEST_RESULTS}/gotestsum-report-exectrace.xml" -- -v -race -coverprofile=coverage.txt -covermode=atomic ./...
test_exit=$?
[[ $test_exit -ne 0 ]] && report_error=1

exit $report_error

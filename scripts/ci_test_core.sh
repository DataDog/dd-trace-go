#!/bin/bash

set -eu

report_error=0

mkdir -p $TEST_RESULTS
PACKAGE_NAMES=$(go list ./... | grep -v /contrib/)

# Set +e so that we run both of the test commands even if
# the first one fails
set +e

export GOEXPERIMENT=synctest # TODO: remove once go1.25 is the minimum supported version

gotestsum --junitfile ${TEST_RESULTS}/gotestsum-report.xml -- $PACKAGE_NAMES -v -race -coverprofile=coverage.txt -covermode=atomic
[[ $? -ne 0 ]] && report_error=1
cd ./internal/exectracetest
gotestsum --junitfile ${TEST_RESULTS}/gotestsum-report-exectrace.xml -- -v -race -coverprofile=coverage.txt -covermode=atomic
[[ $? -ne 0 ]] && report_error=1

exit $report_error

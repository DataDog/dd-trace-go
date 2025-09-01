#!/bin/bash

set -eu

report_error=0
BUILD_TAGS="${BUILD_TAGS:-}"

mkdir -p $TEST_RESULTS
PACKAGE_NAMES=$(go list ./... | grep -v /contrib/)

# Set +e so that we run both of the test commands even if
# the first one fails
set +e

# Build the tags argument if BUILD_TAGS is set
TAGS_ARG=""
if [[ -n "$BUILD_TAGS" ]]; then
  TAGS_ARG="-tags=$BUILD_TAGS"
  echo "Running tests for core packages with build tags: $BUILD_TAGS"
else
  echo "Running standard tests for core packages"
fi

export GOEXPERIMENT=synctest # TODO: remove once go1.25 is the minimum supported version

gotestsum --junitfile ${TEST_RESULTS}/gotestsum-report.xml -- $PACKAGE_NAMES -v -race $TAGS_ARG -coverprofile=coverage.txt -covermode=atomic
[[ $? -ne 0 ]] && report_error=1
cd ./internal/exectracetest
gotestsum --junitfile ${TEST_RESULTS}/gotestsum-report-exectrace.xml -- -v -race $TAGS_ARG -coverprofile=coverage.txt -covermode=atomic
[[ $? -ne 0 ]] && report_error=1

exit $report_error

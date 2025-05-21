#!/bin/bash

set +e

mkdir -p $TEST_RESULTS
PACKAGE_NAMES=$(go list ./... | grep -v /contrib/)
gotestsum --junitfile ${TEST_RESULTS}/gotestsum-report.xml -- $PACKAGE_NAMES -v -race -coverprofile=coverage.txt -covermode=atomic
cd ./internal/exectracetest
gotestsum --junitfile ${TEST_RESULTS}/gotestsum-report-exectrace.xml -- -v -race -coverprofile=coverage.txt -covermode=atomic

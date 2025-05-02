#!/bin/bash

set +e

# This script is used to test the contrib submodules in the apps directory.
# It is run by the GitHub Actions CI workflow defined in
# .github/workflows/unit-integration-tests.yml.

[[ -d ./contrib ]] || exit 0

CONTRIBS=$1

report_error=0

for contrib in $CONTRIBS; do
  echo "Testing contrib module: $contrib"
  contrib_id=$(echo $contrib | sed 's/^\.\///g;s/[\/\.]/_/g')
  cd $contrib
  [[ "$1" = "smoke" ]] && go get -u -t ./...
  gotestsum --junitfile ${TEST_RESULTS}/gotestsum-report-$contrib_id.xml -- ./... -v -race -coverprofile=coverage-$contrib_id.txt -covermode=atomic
  [[ $? -ne 0 ]] && report_error=1
  cd -
done

exit $report_error

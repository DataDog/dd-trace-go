#!/bin/bash

set +e

# This script is used to test the contrib submodules in the apps directory.
# It is run by the GitHub Actions CI workflow defined in
# .github/workflows/unit-integration-tests.yml.

# Arguments are as follows:
# $1 = 'smoke' indicates that we should run smoke tests. Use any other string value to indicate that
# we should not be running smoke tests.
# $2 = CONTRIBS gives a set of contrib directories that we should be testing. If you want to test all
# contribs, you should leave this parameter empty.
# $3 = go-command indicates what go command we should be running. This should have a default value of 'go',
# but can be set to 'gotip' for Go tip testing.

[[ -d ./contrib ]] || exit 0

if [ $# -ne 3 ]; then
  echo "$0 expects to receive three arguments: 'smoke' , 'CONTRIBS', 'go-command'"
  exit 1
fi

# default values, which may be overwritten if our `CONTRIBS` argument is set
CONTRIBS=$(find ./contrib -mindepth 2 -type f -name go.mod -exec dirname {} \;)
INSTRUMENTATION_SUBMODULES=$(find ./instrumentation -mindepth 2 -type f -name go.mod -exec dirname {} \;)

if [ -n "$2" ]; then
  CONTRIBS="$2"
  INSTRUMENTATION_SUBMODULES=""
fi

report_error=0

for contrib in $CONTRIBS; do
  echo "Testing contrib module: $contrib"
  contrib_id=$(echo $contrib | sed 's/^\.\///g;s/[\/\.]/_/g')
  cd $contrib
  [[ "$1" = "smoke" ]] && $3 get -u -t ./...
  gotestsum --junitfile ${TEST_RESULTS}/gotestsum-report-$contrib_id.xml -- ./... -v -race -coverprofile=coverage-$contrib_id.txt -covermode=atomic
  [[ $? -ne 0 ]] && report_error=1
  cd -
done

for mod in $INSTRUMENTATION_SUBMODULES; do
  echo "Testing instrumentation submodule: $mod"
  mod_id=$(echo $mod | sed 's/^\.\///g;s/[\/\.]/_/g')
  cd $mod
  [[ "$1" = "smoke" ]] && $3 get -u -t ./...
  gotestsum --junitfile ${TEST_RESULTS}/gotestsum-report-$mod_id.xml -- ./... -v -race -coverprofile=coverage-$mod_id.txt -covermode=atomic
  [[ $? -ne 0 ]] && report_error=1
  cd -
done

exit $report_error

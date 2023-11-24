#!/bin/bash

set -e

# This script is used to test the contrib submodules in the apps directory.
# It is run by the GitHub Actions CI workflow defined in
# .github/workflows/unit-integration-tests.yml.

CONTRIBS=$(find ./v2/contrib -mindepth 2 -maxdepth 3 -type f -name go.mod -exec dirname {} \;)

for contrib in $CONTRIBS; do
  echo "Testing contrib module: $contrib"
  contrib_id=$(echo $contrib | sed 's/^\.\///g;s/[\/\.]/_/g')
  cd $contrib
  gotestsum --junitfile ${TEST_RESULTS}/gotestsum-report-$contrib_id.xml -- ./... -v -race -coverprofile=coverage-$contrib_id.txt -covermode=atomic
  cd -
done
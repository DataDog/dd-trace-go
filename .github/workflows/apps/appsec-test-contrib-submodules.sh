#!/bin/bash

set -e

# This script is used to test the contrib submodules in the apps directory.
# It is run by the GitHub Actions CI workflow defined in
# .github/workflows/appsec.yml.

if [[ -z "$SCOPE" ]]; then
  gotestsum --junitfile $JUNIT_REPORT.xml -- -v $TO_TEST
else
  cd "./v2/contrib/$SCOPE"
  contrib=$(basename "$SCOPE")
  gotestsum --junitfile "$JUNIT_REPORT.$contrib.xml" -- -v .
fi

#!/bin/bash

set -e

# This script is used to test the contrib submodules in the apps directory.
# It is run by the GitHub Actions CI workflow defined in
# .github/workflows/appsec.yml.

if [[ -z "$SCOPE" ]]; then
  gotestsum --junitfile "$JUNIT_REPORT.xml" -- -v ./appsec/... ./internal/appsec/...
elif [[ "$V2_BRANCH" == "true" ]]; then
  cd "./v2/contrib/$SCOPE"
  contrib=$(basename "$SCOPE")
  gotestsum --junitfile "$JUNIT_REPORT.$contrib.xml" -- -v .
else
  contrib=$(basename "$SCOPE")
  gotestsum --junitfile "$JUNIT_REPORT.$contrib.xml" -- -v "./contrib/$SCOPE/..."
fi

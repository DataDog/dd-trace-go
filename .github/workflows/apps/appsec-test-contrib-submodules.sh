#!/bin/bash

set -e

# This script is used to test the contrib submodules in the apps directory.
# It is run by the GitHub Actions CI workflow defined in
# .github/workflows/appsec.yml.

echo "Running appsec tests for:"
echo "  SCOPE=$SCOPE"
echo "  V2_BRANCH=$V2_BRANCH"
echo "  GODEBUG=$GODEBUG"
echo "  GOEXPERIMENT=$GOEXPERIMENT"
echo "  CGO_ENABLED=$CGO_ENABLED"
echo "  DD_APPSEC_ENABLED=$DD_APPSEC_ENABLED"

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

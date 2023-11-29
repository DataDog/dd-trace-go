#!/bin/bash

set -e

# This script is used to test the contrib submodules in the apps directory.
# It is run by the GitHub Actions CI workflow defined in
# .github/workflows/appsec.yml.

echo "Running appsec tests for:"
echo "  V2_BRANCH=$V2_BRANCH"
echo "  GODEBUG=$GODEBUG"
echo "  GOEXPERIMENT=$GOEXPERIMENT"
echo "  CGO_ENABLED=$CGO_ENABLED"
echo "  DD_APPSEC_ENABLED=$DD_APPSEC_ENABLED"

gotestsum --junitfile "$JUNIT_REPORT.xml" -- -v ./appsec/... ./internal/appsec/...

SCOPES=("gin-gonic/gin" "google.golang.org/grpc" "net/http" "gorilla/mux" "go-chi/chi" "go-chi/chi.v5" "labstack/echo.v4")
for SCOPE in "${SCOPES[@]}"; do
  contrib=$(basename "$SCOPE")
  if [[ "$V2_BRANCH" == "true" ]]; then
    cd "./v2/contrib/$SCOPE"
    gotestsum --junitfile "$JUNIT_REPORT.$contrib.xml" -- -v .
    cd -
  else
    gotestsum --junitfile "$JUNIT_REPORT.$contrib.xml" -- -v "./contrib/$SCOPE/..."
  fi
done

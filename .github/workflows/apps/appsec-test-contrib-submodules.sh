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
echo "  DD_APPSEC_WAF_TIMEOUT=$DD_APPSEC_WAF_TIMEOUT"

function gotestsum_runner() {
  report=$1; shift
  gotestsum --junitfile "$report" -- -v "$@"
}

function docker_runner() {
  # ignore the first argument, which is the JUnit report
  shift
  docker run --platform=$PLATFORM -v $PWD:$PWD -w $PWD -eCGO_ENABLED=$CGO_ENABLED -eDD_APPSEC_ENABLED=$DD_APPSEC_ENABLED -eDD_APPSEC_WAF_TIMEOUT=$DD_APPSEC_WAF_TIMEOUT golang go test -v "$@"
}

runner="gotestsum_runner"
if [[ "$1" == "docker" ]]; then
  runner="docker_runner"; shift
  PLATFORM=$1
  [[ -z "$PLATFORM" ]] && PLATFORM="linux/arm64"
fi

$runner "$JUNIT_REPORT.xml" ./appsec/... ./internal/appsec/...

SCOPES=("gin-gonic/gin" "google.golang.org/grpc" "net/http" "gorilla/mux" "go-chi/chi" "go-chi/chi.v5" "labstack/echo.v4")
for SCOPE in "${SCOPES[@]}"; do
  contrib=$(basename "$SCOPE")
  if [[ "$V2_BRANCH" == "true" ]]; then
    cd "./v2/contrib/$SCOPE"
    $runner "$JUNIT_REPORT.$contrib.xml" .
    cd -
  else
    $runner "$JUNIT_REPORT.$contrib.xml" "./contrib/$SCOPE/..."
  fi
done

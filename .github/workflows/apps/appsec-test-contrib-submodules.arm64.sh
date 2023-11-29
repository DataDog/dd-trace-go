#!/bin/bash

set -e

# This script is used to test the contrib submodules in the apps directory.
# It is run by the GitHub Actions CI workflow defined in
# .github/workflows/appsec.yml.

echo "Running appsec ARM64 tests for:"
echo "  V2_BRANCH=$V2_BRANCH"
echo "  CGO_ENABLED=$CGO_ENABLED"
echo "  DD_APPSEC_ENABLED=$DD_APPSEC_ENABLED"
echo "  DD_APPSEC_WAF_TIMEOUT=$DD_APPSEC_WAF_TIMEOUT"

docker run --platform=linux/arm64 -v $PWD:$PWD -w $PWD -eCGO_ENABLED=$CGO_ENABLED -eDD_APPSEC_ENABLED=$DD_APPSEC_ENABLED -eDD_APPSEC_WAF_TIMEOUT=$DD_APPSEC_WAF_TIMEOUT golang go test -v ./appsec/... ./internal/appsec/...

SCOPES=("gin-gonic/gin" "google.golang.org/grpc" "net/http" "gorilla/mux" "go-chi/chi" "go-chi/chi.v5" "labstack/echo.v4")
for SCOPE in "${SCOPES[@]}"; do
  if [[ "$V2_BRANCH" == "true" ]]; then
    cd "./v2/contrib/$SCOPE"
    docker run --platform=linux/arm64 -v $PWD:$PWD -w $PWD -eCGO_ENABLED=$CGO_ENABLED -eDD_APPSEC_ENABLED=$DD_APPSEC_ENABLED -eDD_APPSEC_WAF_TIMEOUT=$DD_APPSEC_WAF_TIMEOUT golang go test -v .
    cd -
  else
    docker run --platform=linux/arm64 -v $PWD:$PWD -w $PWD -eCGO_ENABLED=$CGO_ENABLED -eDD_APPSEC_ENABLED=$DD_APPSEC_ENABLED -eDD_APPSEC_WAF_TIMEOUT=$DD_APPSEC_WAF_TIMEOUT golang go test -v "./contrib/$SCOPE/..."
  fi
done

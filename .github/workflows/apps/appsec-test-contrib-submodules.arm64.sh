#!/bin/bash

set -e

# This script is used to test the contrib submodules in the apps directory.
# It is run by the GitHub Actions CI workflow defined in
# .github/workflows/appsec.yml.

if [[ -z "$SCOPE" ]]; then
  docker run --platform=linux/arm64 -v $PWD:$PWD -w $PWD -eCGO_ENABLED=$CGO_ENABLED -eDD_APPSEC_ENABLED=$DD_APPSEC_ENABLED -eDD_APPSEC_WAF_TIMEOUT=$DD_APPSEC_WAF_TIMEOUT golang go test -v ./appsec/... ./internal/appsec/...
elif [[ "$V2_BRANCH" == "true" ]]; then
  cd "./v2/contrib/$SCOPE"
  contrib=$(basename "$SCOPE")
  docker run --platform=linux/arm64 -v $PWD:$PWD -w $PWD -eCGO_ENABLED=$CGO_ENABLED -eDD_APPSEC_ENABLED=$DD_APPSEC_ENABLED -eDD_APPSEC_WAF_TIMEOUT=$DD_APPSEC_WAF_TIMEOUT golang go test -v .
else
  contrib=$(basename "$SCOPE")
  docker run --platform=linux/arm64 -v $PWD:$PWD -w $PWD -eCGO_ENABLED=$CGO_ENABLED -eDD_APPSEC_ENABLED=$DD_APPSEC_ENABLED -eDD_APPSEC_WAF_TIMEOUT=$DD_APPSEC_WAF_TIMEOUT golang go test -v "./contrib/$SCOPE/..."
fi

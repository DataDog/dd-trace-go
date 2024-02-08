#!/bin/bash

set -ev

# This script is used to test appsec and its contrib .
# It is run by the GitHub Actions CI workflow defined in
# .github/workflows/appsec.yml.

DOCKER_GOLANG_VERSION=${DOCKER_GOLANG_VERSION:-1}
DOCKER_GOLANG_DISTRIB=${DOCKER_GOLANG_DISTRIB:-alpine}
DOCKER_PLATFORM=${DOCKER_PLATFORM:-linux/amd64}

echo "Running appsec tests for:"
echo "  GODEBUG=$GODEBUG"
echo "  GOEXPERIMENT=$GOEXPERIMENT"
echo "  CGO_ENABLED=$CGO_ENABLED"
echo "  DD_APPSEC_ENABLED=$DD_APPSEC_ENABLED"
echo "  DD_APPSEC_WAF_TIMEOUT=$DD_APPSEC_WAF_TIMEOUT"
echo "  DOCKER_PLATFORM=$DOCKER_PLATFORM"
echo "  DOCKER_GOLANG_VERSION=$DOCKER_GOLANG_VERSION"
echo "  DOCKER_GOLANG_DISTRIB=$DOCKER_GOLANG_DISTRIB"

function docker_runner() {
  cat <<EOF | docker run -i \
    --platform="$DOCKER_PLATFORM" \
    -v "$PWD":"$PWD" -w "$PWD" \
    -v "$GOMODCACHE:$GOMODCACHE" \
    -eGOMODCACHE="$GOMODCACHE" \
    -eCGO_ENABLED="$CGO_ENABLED" \
    -eDD_APPSEC_ENABLED="$DD_APPSEC_ENABLED" \
    -eDD_APPSEC_WAF_TIMEOUT="$DD_APPSEC_WAF_TIMEOUT" \
    golang:$DOCKER_GOLANG_VERSION-$DOCKER_GOLANG_DISTRIB
      go env
      # Install gcc and the libc headers on alpine images
      if [[ $DOCKER_GOLANG_DISTRIB == "alpine" ]]; then
        apk add gcc musl-dev libc6-compat git bash tar
      fi;
      go test -v "$@"
      exit 0
EOF
}

docker_runner ./appsec/... ./internal/appsec/...

CONTRIBS=(
  "gin-gonic/gin" \
  "google.golang.org/grpc" \
  "net/http" "gorilla/mux" \
  "go-chi/chi" "go-chi/chi.v5" \
  "labstack/echo.v4" \
  "99designs/gqlgen" \
  "graphql-go/graphql" \
  "graph-gophers/graphql-go"
)
for CONTRIB in "${CONTRIBS[@]}"; do
  echo "Running appsec tests for contrib/$CONTRIB"
  docker_runner "./contrib/$CONTRIB/..."
done

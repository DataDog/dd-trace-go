#!/bin/bash

CURRENT_DIR=$(dirname "$0")

# shellcheck source=.github/workflows/apps/go-retry.sh
source "${CURRENT_DIR}/../.github/workflows/apps/go-retry.sh"

# The generate commands download modules through the Go module proxy, which
# transiently resets HTTP/2 streams. go-retry.sh retries only those known
# transient failures. Genuine errors still fail immediately.
retry_on_corruption go generate ./...
# "go generate ./..." from the root only reaches root-module packages, so the
# gqlgen test servers in workspace modules are generated separately. The
# _integration server's directive sits behind the gqlgen build tag.
retry_on_corruption go generate -C ./contrib/99designs/gqlgen ./...
retry_on_corruption go generate -C ./internal/orchestrion/_integration -tags=gqlgen ./...
ORCHESTRION_VERSION=$(cat "$CURRENT_DIR/../.orchestrion-version") retry_on_corruption go generate -C ./orchestrion/all -tags=tools ./...

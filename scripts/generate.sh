#!/bin/bash

CURRENT_DIR=$(dirname "$0")

# shellcheck source=.github/workflows/apps/go-retry.sh
source "${CURRENT_DIR}/../.github/workflows/apps/go-retry.sh"

# The generate commands download modules through the Go module proxy, which
# transiently resets HTTP/2 streams. go-retry.sh retries only those known
# transient failures. Genuine errors still fail immediately, and "|| exit" stops
# the script so a failed generator is not masked by a later successful one.
# The root run is not enforced yet: internal/otelprocesscontext needs protoc,
# which the CI generate jobs do not install.
retry_on_corruption go generate ./...
# "go generate ./..." from the root only reaches root-module packages, so the
# gqlgen test servers in workspace modules are generated separately. The
# _integration server's directive sits behind the gqlgen build tag.
retry_on_corruption go generate -C ./contrib/99designs/gqlgen ./... || exit
retry_on_corruption go generate -C ./internal/orchestrion/_integration -tags=gqlgen ./... || exit
ORCHESTRION_VERSION=$(cat "$CURRENT_DIR/../.orchestrion-version") retry_on_corruption go generate -C ./orchestrion/all -tags=tools ./...

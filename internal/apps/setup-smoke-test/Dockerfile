ARG go="1.21" # golang docker image parameter in `golang:{go}-{buildenv}`
ARG build_env="bookworm" # golang docker image parameter in `golang:{go}-{buildenv}`
ARG debian_version="12" # debian docker image versio in `debian:{debianversion}-slim`
ARG deployment_env="debian"

FROM golang:$go-$build_env AS build-env

WORKDIR /src
COPY . .
WORKDIR /src/internal/apps/setup-smoke-test

ARG build_with_cgo="0"
RUN go env -w CGO_ENABLED=$build_with_cgo
ARG build_with_vendoring="" # y or empty
RUN set -ex; if [ "$build_with_vendoring" = "y" ]; then \
      go mod vendor; \
    fi
RUN go env && go build -v -o smoke-test .

# debian deployment environment
# IMPORTANT NOTE: Nothing else than the compiled program must be copied into
# this image to preperly highlight the fact that the compiled program is running
# out of the box in it without any further installation.
FROM debian:$debian_version-slim AS debian
COPY --from=build-env /src/internal/apps/setup-smoke-test/smoke-test /usr/local/bin
CMD /usr/local/bin/smoke-test

# alpine target
# IMPORTANT NOTE: Nothing else than the compiled program must be copied into
# this image to preperly highlight the fact that the compiled program is running
# out of the box in it without any further installation.
FROM alpine AS alpine
COPY --from=build-env /src/internal/apps/setup-smoke-test/smoke-test /usr/local/bin
CMD /usr/local/bin/smoke-test

# redhat target
# IMPORTANT NOTE: Nothing else than the compiled program must be copied into
# this image to preperly highlight the fact that the compiled program is running
# out of the box in it without any further installation.
FROM redhat/ubi8 AS redhat
COPY --from=build-env /src/internal/apps/setup-smoke-test/smoke-test /usr/local/bin
CMD /usr/local/bin/smoke-test

# busybox target
# IMPORTANT NOTE: Nothing else than the compiled program must be copied into
# this image to preperly highlight the fact that the compiled program is running
# out of the box in it without any further installation.
FROM busybox AS busybox
RUN mkdir -p /usr/local/bin
COPY --from=build-env /src/internal/apps/setup-smoke-test/smoke-test /usr/local/bin
CMD /usr/local/bin/smoke-test

# scratch target - meant to be used with CGO_ENABLED=0
# IMPORTANT NOTE: Nothing else than the compiled program must be copied into
# this image to preperly highlight the fact that the compiled program is running
# out of the box in it without any further installation.
FROM scratch AS scratch
COPY --from=build-env /src/internal/apps/setup-smoke-test/smoke-test /
CMD /smoke-test

# Final deployment environment - helper target to end up a single one
FROM $deployment_env AS deployment-env

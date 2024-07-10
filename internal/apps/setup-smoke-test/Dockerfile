# This Dockerfile is used to showcase the onboarding experience of our users
# who leverage docker for their build and deployments.
# It covers both the build requirements and deployment requirements, and it is
# parametrized to allow to covering all the possible combinations we are aware
# of that our users do, with:
# - build environment, made of the following docker build args:
#    - go: the Go version to use, , following their docker image tagging
#      convention `golang:{go}-{buildenv}`.
#    - build_env: the golang build image "environment" to use, following their
#      docker image tagging convention `golang:{go}-{buildenv}`.
#    - build_with_cgo: whether to enable CGO or not (0 or 1).
#    - build_with_vendoring: whether to vendor the Go dependencies with
#      `go mod vendor` or not (y or empty).
# - deployment environment, made of the following docker build args:
#    - deployment_env: the deployment environment to use. Since multiple targets
#      are possible in this multi-stage dockerfile, this parameter allows to
#      select one by default, but also allows to provide a --build-arg option
#      too instead of relying on the --target option. This way, the CI matrix
#      can systematically use --build-arg for all of the parameters.
ARG go="1.21" # golang docker image parameter in `golang:{go}-{buildenv}`
ARG build_env="bookworm" # golang docker image parameter in `golang:{go}-{buildenv}`
ARG build_with_cgo="0" # 0 or 1
ARG build_with_vendoring="" # y or empty
ARG build_tags=""
ARG deployment_env="debian12"

# Build stage compiling the test app in the golang image, along with sub-options
# to possibly enable CGO and vendoring.
FROM golang:$go-$build_env AS build-env

WORKDIR /src
COPY . .
WORKDIR /src/internal/apps/setup-smoke-test

ARG build_with_cgo
RUN go env -w CGO_ENABLED=$build_with_cgo

# GCC and the C library headers are needed for compilation of runtime/cgo with
# CGO_ENABLED=1 - but the golang:alpine image doesn't provide them out of the box.
ARG build_env="bookworm"
RUN set -ex; if [ "$build_env" = "alpine" ] && [ "$build_with_cgo" = "1" ]; then \
      apk update && apk add gcc libc-dev; \
    fi

# If requested, upgrade go-libddwaf to the desired release.
ARG go_libddwaf_ref=""
RUN if [ "${go_libddwaf_ref}" != "" ]; then \
  case "${build_env}" in \
  alpine) apk update && apk add git;; \
  *) apt update && apt install -y git ;; \
  esac; \
  go get -u github.com/DataDog/go-libddwaf/v3@${go_libddwaf_ref}; \
  fi

RUN go mod tidy

ARG build_with_vendoring
RUN set -ex; if [ "$build_with_vendoring" = "y" ]; then \
      go mod vendor; \
    fi
ARG build_tags
RUN go env && go build -v -tags "$build_tags" -o smoke-test .
RUN ldd smoke-test || true

# debian11 deployment environment
# IMPORTANT NOTE: Nothing else than the compiled program must be copied into
# this image to preperly highlight the fact that the compiled program is running
# out of the box in it without any further installation.
FROM debian:11 AS debian11
COPY --from=build-env /src/internal/apps/setup-smoke-test/smoke-test /usr/local/bin
CMD /usr/local/bin/smoke-test

# debian12 deployment environment
# IMPORTANT NOTE: Nothing else than the compiled program must be copied into
# this image to preperly highlight the fact that the compiled program is running
# out of the box in it without any further installation.
FROM debian:12 AS debian12
COPY --from=build-env /src/internal/apps/setup-smoke-test/smoke-test /usr/local/bin
CMD /usr/local/bin/smoke-test

# alpine deployment environment
# IMPORTANT NOTE: Nothing else than the compiled program must be copied into
# this image to preperly highlight the fact that the compiled program is running
# out of the box in it without any further installation.
FROM alpine AS alpine
ARG build_with_cgo
RUN set -ex; if [ "$build_with_cgo" = "1" ]; then \
      apk update && apk add libc6-compat; \
    fi
COPY --from=build-env /src/internal/apps/setup-smoke-test/smoke-test /usr/local/bin
CMD /usr/local/bin/smoke-test

# amazonlinux:2 deployment environment
# IMPORTANT NOTE: Nothing else than the compiled program must be copied into
# this image to preperly highlight the fact that the compiled program is running
# out of the box in it without any further installation.
FROM amazonlinux:2 AS al2
COPY --from=build-env /src/internal/apps/setup-smoke-test/smoke-test /usr/local/bin
CMD /usr/local/bin/smoke-test

# amazonlinux:2023 deployment environment
# IMPORTANT NOTE: Nothing else than the compiled program must be copied into
# this image to preperly highlight the fact that the compiled program is running
# out of the box in it without any further installation.
FROM amazonlinux:2023 AS al2023
COPY --from=build-env /src/internal/apps/setup-smoke-test/smoke-test /usr/local/bin
CMD /usr/local/bin/smoke-test

# busybox deployment environment
# IMPORTANT NOTE: Nothing else than the compiled program must be copied into
# this image to preperly highlight the fact that the compiled program is running
# out of the box in it without any further installation.
FROM busybox AS busybox
RUN mkdir -p /usr/local/bin
COPY --from=build-env /src/internal/apps/setup-smoke-test/smoke-test /usr/local/bin
CMD /usr/local/bin/smoke-test

# scratch deployment environment - meant to be used with CGO_ENABLED=0
# IMPORTANT NOTE: Nothing else than the compiled program must be copied into
# this image to preperly highlight the fact that the compiled program is running
# out of the box in it without any further installation.
FROM scratch AS scratch
COPY --from=build-env /src/internal/apps/setup-smoke-test/smoke-test /
ENTRYPOINT [ "/smoke-test" ]

# Final deployment environment - helper target to end up a single one
FROM $deployment_env AS deployment-env

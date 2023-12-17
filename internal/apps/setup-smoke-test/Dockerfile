# FROM golang:{go}-{buildenv}
ARG go="1.21"
ARG buildenv="alpine"

ARG buildmode="base" # base or go-mod-vendor
ARG debianversion="12" # FROM debian:{debianversion}-slim

FROM golang:$go-$buildenv AS base-build-env

WORKDIR /src
COPY . .
WORKDIR /src/internal/apps/setup-smoke-test

ARG build-with-cgo="0"
ENV CGO_ENABLED=$build-with-cgo

# Aternative base-build-env with vendoring (selected with ARG build-mode)
FROM base-build-env AS go-mod-vendor-build-env
RUN go mod vendor

# $build-mode defaults to base and allows to be changed into go-mod-vendor to
# test this alternative
FROM $buildmode-build-env AS build-env
RUN go build -v -o smoke-test .
RUN pwd && ls -l

# debian deployment environment
FROM debian:$debianversion-slim AS debian
COPY --from=build-env /src/internal/apps/setup-smoke-test/smoke-test /usr/local/bin
CMD /usr/local/bin/smoke-test

# alpine target
FROM alpine AS alpine
COPY --from=build-env /src/internal/apps/setup-smoke-test/smoke-test /usr/local/bin
CMD /usr/local/bin/smoke-test

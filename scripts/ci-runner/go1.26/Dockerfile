# This image pins the digest, following the convention in internal/apps/Dockerfile.
# Dependabot refreshes the digest weekly through the docker group in .github/dependabot.yml.
# make ci/core and make ci/contrib run inside this image for CI-matching Go 1.26 test runs.
FROM golang:1.26-bookworm@sha256:9fdc884aacc3bec89b20ffc69f4bb369c78210e3e4f600387b5128b12c199f81 AS tools
COPY _tools/ /dd-trace-go/_tools/
COPY scripts/install_tools.sh /dd-trace-go/scripts/install_tools.sh
COPY .github/workflows/apps/go-retry.sh /dd-trace-go/.github/workflows/apps/go-retry.sh
WORKDIR /dd-trace-go
RUN ./scripts/install_tools.sh --tools-dir _tools --bin-dir /opt/dd-trace-go-tools/bin

FROM golang:1.26-bookworm@sha256:9fdc884aacc3bec89b20ffc69f4bb369c78210e3e4f600387b5128b12c199f81
COPY --from=tools /opt/dd-trace-go-tools/bin /opt/dd-trace-go-tools/bin
ENV PATH="/opt/dd-trace-go-tools/bin:${PATH}"
# CI's GitHub-hosted runner runs tests as a non-root user, and some tests assert real
# permission enforcement that root would bypass. scripts/ci_runner_run.sh drops to this
# fixed UID with setpriv. setpriv --init-groups requires a real passwd entry, so this user
# needs a home directory. GOPATH and GOCACHE sit under world-writable paths so that UID can
# write to the cache volumes mounted there.
RUN useradd --uid 1000 --create-home ciuser
ENV GOPATH=/go GOCACHE=/go-build HOME=/home/ciuser
RUN mkdir -p /go/pkg/mod /go-build && chmod -R 1777 /go /go-build
WORKDIR /workspace

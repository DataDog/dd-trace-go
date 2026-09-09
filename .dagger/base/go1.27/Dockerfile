FROM golang:1.27.1@sha256:512690a5660563b57d37ecc31129e7f136e831db2aed24a1dbeb8ad7380dc0fa

RUN groupadd --gid 1000 dd-trace-go \
 && useradd --uid 1000 --gid dd-trace-go --create-home --shell /bin/bash dd-trace-go

# _tools/ is a self-contained Go module: only it, install_tools.sh, and the
# go-retry.sh it sources are needed to build the tool binaries into the image.
COPY scripts/install_tools.sh /opt/build-tools/scripts/install_tools.sh
COPY .github/workflows/apps/go-retry.sh /opt/build-tools/.github/workflows/apps/go-retry.sh
COPY _tools /opt/build-tools/_tools
RUN /opt/build-tools/scripts/install_tools.sh --tools-dir /opt/build-tools/_tools --bin-dir /usr/local/bin \
 && rm -rf /opt/build-tools

USER dd-trace-go
WORKDIR /workspace

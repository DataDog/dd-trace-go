FROM golang:1.26.1@sha256:cd78d88e00afadbedd272f977d375a6247455f3a4b1178f8ae8bbcb201743a8a

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

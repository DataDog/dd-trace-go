FROM golang:1.26.1@sha256:cd78d88e00afadbedd272f977d375a6247455f3a4b1178f8ae8bbcb201743a8a

RUN groupadd --gid 1000 dd-trace-go \
 && useradd --uid 1000 --gid dd-trace-go --create-home --shell /bin/bash dd-trace-go

# socat forwards a compose service's published port onto the container's own
# loopback, because dd-trace-go tests dial 127.0.0.1 but a Dagger service is
# only reachable by its alias.
RUN apt-get update && apt-get install -y --no-install-recommends socat \
 && rm -rf /var/lib/apt/lists/*

# checkmake has no Go install path, unlike the rest of _tools/tools.go, so
# scripts/lint.sh --misc needs it fetched as a prebuilt binary instead.
RUN case "$(dpkg --print-architecture)" in \
      amd64) CHECKMAKE_ARCH=amd64 ;; \
      arm64) CHECKMAKE_ARCH=arm64 ;; \
      *) echo "unsupported architecture: $(dpkg --print-architecture)" >&2; exit 1 ;; \
    esac \
 && curl -fsSL -o /usr/local/bin/checkmake \
      "https://github.com/checkmake/checkmake/releases/download/0.2.2/checkmake-0.2.2.linux.${CHECKMAKE_ARCH}" \
 && chmod +x /usr/local/bin/checkmake

# _tools/ is a self-contained Go module: only it, install_tools.sh, and the
# go-retry.sh it sources are needed to build the tool binaries into the image.
COPY scripts/install_tools.sh /opt/build-tools/scripts/install_tools.sh
COPY .github/workflows/apps/go-retry.sh /opt/build-tools/.github/workflows/apps/go-retry.sh
COPY _tools /opt/build-tools/_tools
RUN /opt/build-tools/scripts/install_tools.sh --tools-dir /opt/build-tools/_tools --bin-dir /usr/local/bin \
 && rm -rf /opt/build-tools

# install_tools.sh already populated /go/pkg/mod as root; a real test run
# adds packages install_tools.sh never touched, so the non-root user needs
# write access to the same cache rather than a fresh one.
RUN chown -R dd-trace-go:dd-trace-go /go

USER dd-trace-go
WORKDIR /workspace

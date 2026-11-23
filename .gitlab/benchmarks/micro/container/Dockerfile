# GitLab CI blocks Docker Hub, so this image comes from the internal mirror.
# Dependabot cannot reach the mirror. Update the digest by hand when you rebuild the image (see ../README.md).
FROM registry.ddbuild.io/images/mirror/golang:1.27@sha256:2920bb124f1a2e12a1d39f0cab756ae0068441ee1da7655dec7f5e374944095b

ARG CI_JOB_TOKEN

ENV PYTHONUNBUFFERED 1
ENV PYTHONDONTWRITEBYTECODE 1
ENV DEBIAN_FRONTEND noninteractive
ENV PYTHON_VERSION 3.9.15

ENV PYENV_ROOT "/pyenv"
ENV PATH "$PYENV_ROOT/shims:$PYENV_ROOT/bin:/app:/app/bp-runner/:/app/benchmark-github-tools/:/app/benchmark-analyzer/:$PATH"

SHELL ["/bin/bash", "-c"]

COPY install-base-deps.sh ./install-base-deps.sh
RUN --mount=type=ssh source ./install-base-deps.sh

COPY install-bp-tools.sh ./install-bp-tools.sh
RUN --mount=type=ssh source ./install-bp-tools.sh
RUN --mount=type=ssh source bp-install bp-runner bp-analyzer bp-github

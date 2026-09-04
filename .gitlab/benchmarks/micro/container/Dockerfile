FROM golang:1.27@sha256:4013ae0f9e7994f8535c58c811f8f863fbed38b72e0d51e6592156f758d66146

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

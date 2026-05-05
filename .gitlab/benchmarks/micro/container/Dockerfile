FROM golang:1.25@sha256:8a7adc288b77e9b787cd2695029eb54d10ae80571b21d44fed68d067ad0a9c96

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
RUN --mount=type=ssh source bp-install bp-analyzer bp-github bp-runner

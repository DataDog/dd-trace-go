#!/usr/bin/env bash

set -ex

if [ -z "$PYENV_ROOT" ]; then
   echo "FAIL! Make sure PYENV_ROOT is set!"
   exit 1
fi

apt-get update \
    && apt-get -y install --no-install-recommends \
        apt-transport-https \
        ca-certificates \
        openssl \
        tzdata \
        git \
        openssh-client \
        zip \
        curl \
        jq \
        curl \
        hwinfo procps \
        build-essential \
        uuid-runtime \
        apache2-utils \
        make libssl-dev zlib1g-dev \
        libbz2-dev libreadline-dev libsqlite3-dev wget curl llvm \
        libncursesw5-dev xz-utils tk-dev libxml2-dev libxmlsec1-dev libffi-dev liblzma-dev \
    && apt-get -y clean \
    && rm -rf /var/lib/apt/lists/*

git clone --depth 1 https://github.com/pyenv/pyenv.git --branch "v2.4.0" --single-branch /pyenv
eval "$(pyenv init -)"
pyenv install "$PYTHON_VERSION" && pyenv global "$PYTHON_VERSION"

pip3 install awscli virtualenv setuptools

echo "Copying bp-install from benchmarking-platform-tools repo..."

if [ ! -d "/tmp/benchmarking-platform-tools" ]; then
  if [ -n "$CI_JOB_TOKEN" ]; then
    git clone https://gitlab-ci-token:${CI_JOB_TOKEN}@gitlab.ddbuild.io/DataDog/benchmarking-platform-tools /tmp/benchmarking-platform-tools
  else
    mkdir -p ~/.ssh && ssh-keyscan -t rsa github.com >> ~/.ssh/known_hosts
    git clone git@github.com:DataDog/benchmarking-platform-tools /tmp/benchmarking-platform-tools
  fi
fi

mkdir -p /app
cp -r /tmp/benchmarking-platform-tools/images/templates/linux/bp-install /app/bp-install

echo "Success!"

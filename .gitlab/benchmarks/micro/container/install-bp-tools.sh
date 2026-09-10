#!/usr/bin/env bash

set -ex

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

echo "Successfully installed bp-install!"

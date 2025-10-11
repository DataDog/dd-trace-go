#!/usr/bin/env bash

set -euo pipefail

docker run \
  --rm \
  --interactive \
  --tty \
  --volume "$(pwd):/mnt" \
  koalaman/shellcheck:stable scripts/*.sh

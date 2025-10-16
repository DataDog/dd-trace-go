#!/usr/bin/env bash

set -euo pipefail

# Default to scripts/*.sh if no arguments provided
SCRIPTS="${*:-scripts/*.sh}"

# Check if shellcheck is available locally
if command -v shellcheck > /dev/null 2>&1; then
  echo "Using local shellcheck binary"
  # shellcheck disable=SC2086
  shellcheck $SCRIPTS
else
  echo "Using Docker to run shellcheck"
  # shellcheck disable=SC2086
  docker run \
    --rm \
    --volume "$(pwd):/mnt" \
    koalaman/shellcheck:stable $SCRIPTS
fi

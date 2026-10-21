#!/usr/bin/env bash
# gomod-key.sh — prints "tag=gomod-<sha256>" for the GHCR module-cache seed.
#
# The key is a hash of the git *blob IDs* and paths of every go.sum, go.work
# and go.work.sum in the repository. Blob IDs are used, not file contents, so
# CRLF conversion on a Windows checkout can never change the key. The publish
# and consume sides run this same script, so they always agree on the tag.
set -euo pipefail

cd "${GITHUB_WORKSPACE:-$(git rev-parse --show-toplevel)}"

h() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum
  else
    shasum -a 256
  fi
}

# `git ls-files -s` prints: <mode> <blob-sha> <stage>\t<path>
# Sorting with a fixed locale keeps the order stable across platforms.
sum=$(git ls-files -s -- ':(glob)**/go.sum' ':(glob)go.work' ':(glob)go.work.sum' \
      | LC_ALL=C sort | h | cut -c1-64)

echo "tag=gomod-${sum}"

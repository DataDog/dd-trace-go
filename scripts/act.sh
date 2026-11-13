#!/bin/bash

set -e

usage() {
  cat << EOF
Usage: $(basename "${BASH_SOURCE[0]}") [act args...]

Thin wrapper around \`act\` (https://github.com/nektos/act): checks that act and Docker are
available, then forwards every argument straight through to act. Flags in .actrc (platform
mappings, --network, --artifact-server-path) apply automatically since act reads it from the
working directory.

See .github/act/README.md for what act validates, and what it does not, before you rely on it.

Examples:
  $(basename "${BASH_SOURCE[0]}") -W .github/workflows/generate.yml -j generate
  $(basename "${BASH_SOURCE[0]}") -W .github/workflows/static-checks.yml -j checklocks
  $(basename "${BASH_SOURCE[0]}") -l                          # list every job act can see
EOF
  exit 0
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
fi

if ! command -v act > /dev/null 2>&1; then
  echo "act is required: brew install act (see .github/act/README.md)" >&2
  exit 1
fi

if ! docker info > /dev/null 2>&1; then
  echo "Docker must be running to use act" >&2
  exit 1
fi

exec act "$@"

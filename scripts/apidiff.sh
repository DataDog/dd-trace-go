#!/usr/bin/env bash
# Unless explicitly stated otherwise all files in this repository are licensed
# under the Apache License Version 2.0.
# This product includes software developed at Datadog (https://www.datadoghq.com/).
# Copyright 2025 Datadog, Inc.
set -euo pipefail

# apidiff.sh — compare the public API of Go packages between a base git ref
# and the current working tree using golang.org/x/exp/cmd/apidiff.
#
# Usage: scripts/apidiff.sh [OPTIONS] PACKAGE_IMPORT_PATH...
#
# Options:
#   --base-ref REF        Git ref to compare against (default: origin/main, or $APIDIFF_BASE_REF)
#   --incompatible-only   Show only incompatible (breaking) changes
#   --exit-code           Exit 1 if incompatible changes are found
#   -h, --help            Show this help message
#
# Examples:
#   scripts/apidiff.sh github.com/DataDog/dd-trace-go/v2/ddtrace/tracer
#   scripts/apidiff.sh --base-ref origin/main --exit-code github.com/DataDog/dd-trace-go/v2/ddtrace/tracer
#   scripts/apidiff.sh --incompatible-only --exit-code github.com/DataDog/dd-trace-go/v2/ddtrace/tracer

usage() {
  cat << EOF
Usage: $(basename "${BASH_SOURCE[0]}") [OPTIONS] PACKAGE_IMPORT_PATH...

Compare the public API of Go packages between a base git ref and the current
working tree using golang.org/x/exp/cmd/apidiff.

Options:
  --base-ref REF        Git ref to compare against (default: origin/main, or \$APIDIFF_BASE_REF)
  --incompatible-only   Show only incompatible (breaking) changes
  --exit-code           Exit 1 if incompatible changes are found
  -h, --help            Show this help message

Examples:
  $(basename "${BASH_SOURCE[0]}") github.com/DataDog/dd-trace-go/v2/ddtrace/tracer
  $(basename "${BASH_SOURCE[0]}") --base-ref origin/release/v2.9 --exit-code github.com/DataDog/dd-trace-go/v2/ddtrace/tracer
  $(basename "${BASH_SOURCE[0]}") --incompatible-only github.com/DataDog/dd-trace-go/v2/ddtrace/tracer
EOF
  exit 0
}

# Defaults
BASE_REF="${APIDIFF_BASE_REF:-origin/main}"
INCOMPATIBLE_ONLY=false
EXIT_CODE=false
PACKAGES=()

# Parse arguments
while [[ $# -gt 0 ]]; do
  case "$1" in
    --base-ref)
      BASE_REF="$2"
      shift 2
      ;;
    --incompatible-only)
      INCOMPATIBLE_ONLY=true
      shift
      ;;
    --exit-code)
      EXIT_CODE=true
      shift
      ;;
    -h | --help)
      usage
      ;;
    -*)
      echo "Error: unknown option: $1" >&2
      usage
      ;;
    *)
      PACKAGES+=("$1")
      shift
      ;;
  esac
done

if [[ ${#PACKAGES[@]} -eq 0 ]]; then
  echo "Error: at least one package import path is required" >&2
  usage
fi

# Locate the apidiff binary
APIDIFF=""
if [[ -x "./bin/apidiff" ]]; then
  APIDIFF="$(pwd)/bin/apidiff"
elif command -v apidiff > /dev/null 2>&1; then
  APIDIFF="$(command -v apidiff)"
else
  echo "Error: apidiff binary not found." >&2
  echo "Run 'make tools-install' to install it." >&2
  exit 1
fi

# Find the repo root
REPO_ROOT="$(git rev-parse --show-toplevel)"

# Prepare a temp dir and register cleanup on exit
WORK_DIR="$(mktemp -d)"
WORKTREE_DIR="${WORK_DIR}/base"

cleanup() {
  git -C "${REPO_ROOT}" worktree remove --force "${WORKTREE_DIR}" 2> /dev/null || true
  rm -rf "${WORK_DIR}"
}
trap cleanup EXIT

# Fetch the base ref quietly (best-effort; may already be present)
REF_NAME="${BASE_REF#origin/}"
git -C "${REPO_ROOT}" fetch origin "${REF_NAME}" --no-tags --quiet 2> /dev/null || true

# Check out the base ref into a temporary worktree
git -C "${REPO_ROOT}" worktree add --detach --quiet "${WORKTREE_DIR}" "${BASE_REF}"

# Download module dependencies in the base worktree so apidiff can type-check
(
  cd "${WORKTREE_DIR}"
  GOWORK=off go mod download -x 2> /dev/null || go mod download
)

# Compare each package
HAS_INCOMPATIBLE=false

for PKG in "${PACKAGES[@]}"; do
  EXPORT_FILE="${WORK_DIR}/$(echo "${PKG}" | tr '/' '_').apidata"

  # Export the API from the base ref
  (
    cd "${WORKTREE_DIR}"
    "${APIDIFF}" -w "${EXPORT_FILE}" "${PKG}"
  )

  # Compute display output (full or incompatible-only)
  if [[ "${INCOMPATIBLE_ONLY}" == "true" ]]; then
    OUTPUT="$(cd "${REPO_ROOT}" && "${APIDIFF}" -incompatible "${EXPORT_FILE}" "${PKG}")"
  else
    OUTPUT="$(cd "${REPO_ROOT}" && "${APIDIFF}" "${EXPORT_FILE}" "${PKG}")"
  fi

  # Print results for this package
  if [[ -n "${OUTPUT}" ]]; then
    echo "=== ${PKG} ==="
    echo "${OUTPUT}"
    echo ""
  fi

  # Check for incompatible changes (for --exit-code gate)
  if [[ "${EXIT_CODE}" == "true" ]]; then
    if [[ "${INCOMPATIBLE_ONLY}" == "true" ]]; then
      # OUTPUT already contains only incompatible changes
      INCOMP_OUTPUT="${OUTPUT}"
    else
      INCOMP_OUTPUT="$(cd "${REPO_ROOT}" && "${APIDIFF}" -incompatible "${EXPORT_FILE}" "${PKG}")"
    fi
    if [[ -n "${INCOMP_OUTPUT}" ]]; then
      HAS_INCOMPATIBLE=true
    fi
  fi
done

if [[ "${HAS_INCOMPATIBLE}" == "true" ]]; then
  # Exit 2 signals "incompatible changes found" — distinct from exit 1 which
  # indicates an operational failure (git, go mod, apidiff binary errors).
  # Callers can use this to differentiate "gate triggered" from "tool broken".
  exit 2
fi

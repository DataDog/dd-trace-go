#!/usr/bin/env bash
# Upgrade the Orchestrion dependency across all configured modules.

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

REQUESTED_VERSION="${1:-}"
if [[ -n "$REQUESTED_VERSION" ]]; then
  ORCHESTRION_VERSION="$REQUESTED_VERSION"
else
  ORCHESTRION_VERSION="${ORCHESTRION_VERSION:-latest}"
fi

ORCHESTRION_DIRS="${ORCHESTRION_DIRS:-internal/orchestrion/_integration orchestrion/all}"

echo "Checking Orchestrion upgrade to ${ORCHESTRION_VERSION}"
TARGET_VERSION="$(cd "$ROOT_DIR" && go list -m "github.com/DataDog/orchestrion@${ORCHESTRION_VERSION}" 2> /dev/null | awk '{print $2}')"
if [[ -z "$TARGET_VERSION" ]]; then
  echo "Error: Could not resolve Orchestrion version ${ORCHESTRION_VERSION}"
  exit 1
fi
echo "Target version: ${TARGET_VERSION}"

NEEDS_UPGRADE=false
for dir in ${ORCHESTRION_DIRS}; do
  module_dir="${ROOT_DIR}/${dir}"
  if [[ ! -d "$module_dir" ]]; then
    echo "${dir}: Directory not found, skipping"
    continue
  fi

  CURRENT_VERSION=""
  if ! CURRENT_VERSION="$(cd "$module_dir" && go list -m github.com/DataDog/orchestrion 2> /dev/null | awk '{print $2}')"; then
    CURRENT_VERSION=""
  fi

  if [[ "$CURRENT_VERSION" == "$TARGET_VERSION" ]]; then
    echo "${dir}: Already at version ${TARGET_VERSION}"
    continue
  fi

  NEWER="$(printf '%s\n%s\n' "$CURRENT_VERSION" "$TARGET_VERSION" | sort -V | tail -n1)"
  if [[ "$NEWER" == "$CURRENT_VERSION" && "$CURRENT_VERSION" != "$TARGET_VERSION" ]]; then
    echo "${dir}: Current version ${CURRENT_VERSION} is newer than target ${TARGET_VERSION}, skipping"
    continue
  fi

  echo "${dir}: Current version ${CURRENT_VERSION} will be upgraded to ${TARGET_VERSION}"
  NEEDS_UPGRADE=true
done

if [[ "$NEEDS_UPGRADE" == "false" ]]; then
  echo "All modules already at or newer than target version ${TARGET_VERSION}, skipping upgrade"
  exit 0
fi

echo "Upgrading Orchestrion to ${TARGET_VERSION}"
for dir in ${ORCHESTRION_DIRS}; do
  module_dir="${ROOT_DIR}/${dir}"
  if [[ ! -d "$module_dir" ]]; then
    continue
  fi

  (
    echo "Upgrading Orchestrion in ${dir}"
    cd "$module_dir"
    go get "github.com/DataDog/orchestrion@${ORCHESTRION_VERSION}"
    go mod tidy
    go mod verify
    echo "Orchestrion upgraded in ${dir}"
  )
done

make -C "$ROOT_DIR" fix-modules
echo "${TARGET_VERSION}" > "${ROOT_DIR}/.orchestrion-version"
echo "Orchestrion upgrade complete"

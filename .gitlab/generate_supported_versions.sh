#!/usr/bin/env bash
# Writes supported_versions.json at the repository root for the one-pipeline
# generate-supported-versions job. See scripts/gensupportedversions.
set -euo pipefail

cd "$(dirname "$0")/.."
# shellcheck source=../.github/workflows/apps/go-retry.sh
. ./.github/workflows/apps/go-retry.sh

# The generator only needs the root module, so skip loading every go.work module.
export GOWORK=off
retry_on_corruption go run ./scripts/gensupportedversions

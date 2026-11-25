#!/bin/bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091 source=.github/workflows/apps/go-retry.sh
source "${SCRIPT_DIR}/../.github/workflows/apps/go-retry.sh"

retry_on_corruption go run -tags=scripts ./scripts/fixmodules -root=. .

# Tidy modules that scripts/fixmodules/main.go does not cover: skipModules,
# dot-prefixed directories excluded by findModules, and testdata fixtures
# shadowed by a duplicate module path.
for dir in _tools tools/v2fix .github/workflows/apps .gitlab scripts/errtrackaudit/testdata/dotdir; do
  retry_on_corruption go -C "$dir" mod tidy
done

# This command will update the go.work.sum file
go list -m all > /dev/null

#!/bin/bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=.github/workflows/apps/go-retry.sh
source "${SCRIPT_DIR}/../.github/workflows/apps/go-retry.sh"

# This scripts runs go mod tidy on all the go modules of the repo, and additionally it adds missing replace directives
# for local imports.

retry_on_corruption go run -tags=scripts ./scripts/fixmodules -root=. .

while IFS= read -r -d '' f; do
  (
    cd "$(dirname "$f")" || exit 1
    retry_on_corruption go mod tidy
  )
done < <(find . -name go.mod -print0)

# This command will update the go.work.sum file
go list -m all > /dev/null

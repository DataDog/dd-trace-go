#!/bin/bash

set -euo pipefail

# This scripts runs go mod tidy on all the go modules of the repo, and additionally it adds missing replace directives
# for local imports.

go run -tags=scripts ./scripts/fixmodules -root=. .

while IFS= read -r -d '' f; do
  (
    cd "$(dirname "$f")" || exit 1
    go mod tidy
  )
done < <(find . -name go.mod -print0)

# This command will update the go.work.sum file
go list -m all > /dev/null

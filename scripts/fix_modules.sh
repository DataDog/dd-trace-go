#!/bin/bash

set -euo pipefail

# This scripts runs go mod tidy on all the go modules of the repo, and additionally it adds missing replace directives
# for local imports.

go run -tags=scripts ./scripts/fixmodules -root=. .

for f in $(find . -name go.mod); do
	(
		cd $(dirname $f)
		go mod tidy
	)
done

# This command will update the go.work.sum file
go list -m all >/dev/null

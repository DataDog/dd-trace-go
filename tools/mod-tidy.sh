#/bin/bash

set -e
set -o pipefail

for f in $(find . -name go.mod)
do
  echo "$(dirname $f)"
  cd $(dirname $f)
  go mod edit -go=1.21
  go mod tidy
  cd -
  # (cd $(dirname $f); go mod tidy)
done

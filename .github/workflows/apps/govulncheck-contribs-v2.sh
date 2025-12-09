#!/bin/bash

find ./contrib -mindepth 2 -type f -name go.mod -exec dirname {} \; | while read dir ; do
  echo "Checking $dir"
  # govulncheck doesn't support modules with only a go.mod in the root
  go_files=$(find $dir -maxdepth 1 -type f -name '*.go' | wc -l)
  [[ $go_files -eq 0 ]] && dir=$(realpath "$(ls -d $dir/*/ | head)")

  govulncheck -C $dir .
done
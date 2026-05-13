#!/bin/bash

# Use go.work as the authoritative module list — avoids nested test go.mod files.
grep -E '^\s+\./contrib/' go.work | awk '{print $1}' | while read -r dir; do
  echo "Checking $dir"
  # govulncheck doesn't support modules with only a go.mod in the root
  go_files=$(find $dir -maxdepth 1 -type f -name '*.go' | wc -l)
  [[ $go_files -eq 0 ]] && dir=$(realpath "$(ls -d $dir/*/ | head)")

  govulncheck -C $dir .
done
#!/bin/bash
set -euo pipefail

# Scans each contrib module with govulncheck in SARIF format and merges the
# results into a single SARIF file for upload to GitHub Code Scanning.
#
# Usage: govulncheck-contribs-sarif.sh [output-file]
#   output-file  Path for the merged SARIF output (default: govulncheck-contribs.sarif)
#
# Requires: govulncheck, jq

OUTPUT="${1:-govulncheck-contribs.sarif}"
SARIF_DIR=$(mktemp -d)
trap 'rm -rf "$SARIF_DIR"' EXIT

count=0
find ./contrib -mindepth 2 -type f -name go.mod -exec dirname {} \; | while read -r dir; do
  echo "Scanning $dir"
  # govulncheck requires at least one .go file in the target directory;
  # fall back to the first subdirectory when the module root has none.
  go_files=$(find "$dir" -maxdepth 1 -type f -name '*.go' | wc -l)
  [[ $go_files -eq 0 ]] && dir=$(realpath "$(ls -d "$dir"/*/ | head -1)")

  safe_name=$(printf '%s' "$dir" | tr '/' '_' | sed 's/^_//')
  # -format sarif exits 0 even when vulnerabilities are found.
  govulncheck -format sarif -C "$dir" . >"$SARIF_DIR/${safe_name}.sarif"
  count=$((count + 1))
done

sarif_files=("$SARIF_DIR"/*.sarif)
if [[ ! -e "${sarif_files[0]}" ]]; then
  echo "No contrib modules found; skipping SARIF merge."
  exit 0
fi

# Merge all per-module SARIF runs into one file.
# All runs share the same govulncheck schema version, so the first file's
# version and $schema fields are representative for the merged output.
jq -s '{
  "version": .[0].version,
  "$schema": (.[0]."$schema" // ""),
  "runs": [.[].runs[]]
}' "$SARIF_DIR"/*.sarif >"$OUTPUT"

echo "Merged $(echo "$SARIF_DIR"/*.sarif | wc -w) SARIF files into $OUTPUT"

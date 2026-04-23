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
# Use go.work as the authoritative module list — avoids picking up nested
# test-only go.mod files (e.g. contrib/aws/datadog-lambda-go/test/...) that
# are not workspace members and cause govulncheck package-load errors.
grep -E '^\s+\./contrib/' go.work | awk '{print $1}' | while read -r dir; do
  echo "Scanning $dir"
  # Capture the module path (repo-root-relative) before any fallback rewrites
  # $dir — used as the URI prefix when rewriting SARIF paths below.
  module_dir="${dir#./}"  # strip leading "./" → "contrib/aws/aws-sdk-go"

  # govulncheck requires at least one .go file in the target directory;
  # fall back to the first subdirectory when the module root has none.
  go_files=$(find "$dir" -maxdepth 1 -type f -name '*.go' | wc -l)
  [[ $go_files -eq 0 ]] && dir=$(realpath "$(ls -d "$dir"/*/ | head -1)")

  safe_name=$(printf '%s' "$module_dir" | tr '/' '_')
  # -format sarif exits 0 even when vulnerabilities are found.
  govulncheck -format sarif -C "$dir" . >"$SARIF_DIR/${safe_name}.sarif"

  # Rewrite URIs to be repo-root-relative so Code Scanning annotations resolve
  # correctly. govulncheck emits paths relative to the module dir with
  # uriBaseId="%SRCROOT%". Prefix each with the module dir and drop uriBaseId
  # so the merged single-run file has unambiguous repo-root-relative paths.
  jq --arg prefix "${module_dir}/" '
    walk(
      if type == "object" and (.uriBaseId? == "%SRCROOT%") and has("uri")
      then .uri = ($prefix + .uri) | del(.uriBaseId)
      else .
      end
    )
  ' "$SARIF_DIR/${safe_name}.sarif" >"$SARIF_DIR/${safe_name}.tmp"
  mv "$SARIF_DIR/${safe_name}.tmp" "$SARIF_DIR/${safe_name}.sarif"

  count=$((count + 1))
done

sarif_files=("$SARIF_DIR"/*.sarif)
if [[ ! -e "${sarif_files[0]}" ]]; then
  echo "No contrib modules found; skipping SARIF merge."
  exit 0
fi

# Merge all per-module SARIF files into one file with a single run.
# CodeQL upload-sarif rejects files with multiple runs under the same category
# (https://github.blog/changelog/2025-07-21-code-scanning-will-stop-combining-multiple-sarif-runs-uploaded-in-the-same-sarif-file/).
# govulncheck uses URI-based artifact locations (not index-based), so merging
# results across runs is safe — no artifact re-indexing required.
#
# tool.driver.rules is merged and deduplicated across all runs: each run only
# carries the rules referenced by its own results, so a naïve first-wins merge
# would lose rule descriptors (title, help) for vulns found in later modules.
jq -s '
  . as $all |
  {
    "version": .[0].version,
    "$schema": (.[0]."$schema" // ""),
    "runs": [{
      "tool": (
        .[0].runs[0].tool |
        .driver.rules = ([$all[].runs[].tool.driver.rules[]?] | unique_by(.id))
      ),
      "results": [$all[].runs[].results[]?]
    }]
  }
' "$SARIF_DIR"/*.sarif >"$OUTPUT"

echo "Merged $(echo "$SARIF_DIR"/*.sarif | wc -w) SARIF files into $OUTPUT"

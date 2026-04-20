#!/usr/bin/env bash
# govulncheck-fix.sh: Scan all modules for reachable vulnerabilities, apply
# available fixes by updating affected go.mod files, and report results.
#
# Relies on govulncheck@latest being installed and on PATH.
#
# Outputs (via GITHUB_OUTPUT):
#   has_fixes=true|false
#
# Side effects:
#   - Modifies go.mod/go.sum files for modules with vulnerable dependencies
#   - Writes /tmp/govulncheck-fix-commit.txt (used by git commit -F)
#   - Writes /tmp/govulncheck-fix-body.md (used as PR description)

set -euo pipefail

FIXES_FILE=$(mktemp)
readonly FIXES_FILE
GITHUB_OUTPUT="${GITHUB_OUTPUT:-/dev/null}"

# ── parse_findings ─────────────────────────────────────────────────────────────
# Reads govulncheck streaming JSON from stdin and extracts "module fixedVersion"
# pairs for findings that have an available fix.
#
# govulncheck JSON emits a series of Message objects (one per line). Each
# Message may contain a Finding. Finding.trace[-1] is the vulnerable symbol;
# its .module is the third-party module that contains the vulnerability and
# should be upgraded to .fixed_version.
#
# We skip stdlib vulnerabilities (module == "stdlib") since those are fixed
# by upgrading Go itself, not via go get.
parse_findings() {
  jq -r '
    select(.finding != null) |
    select(.finding.fixed_version != null and .finding.fixed_version != "") |
    select(.finding.trace != null and (.finding.trace | length) > 0) |
    select(.finding.trace[-1].module != null and .finding.trace[-1].module != "") |
    select(.finding.trace[-1].module != "stdlib") |
    .finding.trace[-1].module + " " + .finding.fixed_version
  '
}

# ── Scan core packages ─────────────────────────────────────────────────────────
echo "==> Scanning core packages..."
govulncheck -json \
  ./ddtrace/... ./appsec/... ./profiler/... ./internal/... ./instrumentation/... \
  2>/dev/null | parse_findings >> "${FIXES_FILE}" || true

# ── Scan contrib modules ───────────────────────────────────────────────────────
echo "==> Scanning contrib modules..."
while IFS= read -r -d '' gomod; do
  dir=$(dirname "${gomod}")

  # govulncheck requires at least one .go file in the target directory.
  go_files=$(find "${dir}" -maxdepth 1 -type f -name '*.go' | wc -l)
  [[ "${go_files}" -eq 0 ]] && dir=$(realpath "$(find "${dir}" -mindepth 1 -maxdepth 1 -type d | head -1)")

  echo "  Checking ${dir}"
  govulncheck -C "${dir}" -json . 2>/dev/null | parse_findings >> "${FIXES_FILE}" || true
done < <(find ./contrib -mindepth 2 -type f -name go.mod -print0)

# ── Check for fixes ────────────────────────────────────────────────────────────
if [[ ! -s "${FIXES_FILE}" ]]; then
  echo "No reachable vulnerabilities with available fixes found."
  echo "has_fixes=false" >> "${GITHUB_OUTPUT}"
  rm -f "${FIXES_FILE}"
  exit 0
fi

# Deduplicate: for the same module, keep only the highest fixed version.
# sort -k2,2V sorts by semver (GNU sort extension, available on Ubuntu runners).
sort -u "${FIXES_FILE}" \
  | sort -t' ' -k1,1 -k2,2Vr \
  | awk '!seen[$1]++' \
  > "${FIXES_FILE}.dedup"
mv "${FIXES_FILE}.dedup" "${FIXES_FILE}"

echo ""
echo "==> Vulnerabilities with fixes:"
cat "${FIXES_FILE}"
echo ""

echo "has_fixes=true" >> "${GITHUB_OUTPUT}"
{
  echo "dependency_list<<EOF"
  cat "${FIXES_FILE}"
  echo "EOF"
} >> "${GITHUB_OUTPUT}"

out=$(awk 'NR>1{printf "\\n"} {printf "%s", $0}' "${FIXES_FILE}")
echo "dependency_list=${out}" >> "${GITHUB_OUTPUT}"
rm -f "${FIXES_FILE}"

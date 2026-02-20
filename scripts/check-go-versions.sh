#!/bin/bash
# check-go-versions.sh: Verify go.mod/go.work versions are consistent with go-versions.yml.
#
# Checks:
#   1. go.work declares the expected oldstable_patch version
#   2. All go.mod files declare the expected oldstable_patch version
#   3. _tools/go.mod Go version (used to build golangci-lint) — warns if out of sync
#
# Exit codes:
#   0  all checks passed (or warnings only, without --strict)
#   1  one or more failures (or warnings with --strict)
#
# Usage:
#   ./scripts/check-go-versions.sh           # report; exit 0 on warnings
#   ./scripts/check-go-versions.sh --strict  # exit 1 on warnings too

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
STRICT=false

for arg in "$@"; do
  case "$arg" in
    --strict) STRICT=true ;;
    *) printf 'Unknown argument: %s\nUsage: %s [--strict]\n' "$arg" "$0" >&2; exit 1 ;;
  esac
done

VERSIONS_FILE="${REPO_ROOT}/go-versions.yml"
if [[ ! -f "$VERSIONS_FILE" ]]; then
  printf 'ERROR: go-versions.yml not found at %s\n' "$VERSIONS_FILE" >&2
  exit 1
fi

# Parse go-versions.yml
stable=$(grep '^stable:' "$VERSIONS_FILE" | sed 's/.*"\(.*\)".*/\1/')
oldstable=$(grep '^oldstable:' "$VERSIONS_FILE" | sed 's/.*"\(.*\)".*/\1/')
stable_patch=$(grep '^stable_patch:' "$VERSIONS_FILE" | sed 's/.*"\(.*\)".*/\1/')
oldstable_patch=$(grep '^oldstable_patch:' "$VERSIONS_FILE" | sed 's/.*"\(.*\)".*/\1/')

printf 'Checking Go version consistency against go-versions.yml:\n'
printf '  stable:    %s (%s)\n' "$stable" "$stable_patch"
printf '  oldstable: %s (%s)\n' "$oldstable" "$oldstable_patch"
printf '  Expected version in go.mod/go.work: %s\n\n' "$oldstable_patch"

failures=0
warnings=0

fail() { printf '  FAIL: %s\n' "$*" >&2; ((failures++)) || true; }
warn() { printf '  WARN: %s\n' "$*"; ((warnings++)) || true; }
ok()   { printf '  ok:   %s\n' "$*"; }

# ----- Check go.work -----
printf 'go.work:\n'
go_work_version=$(grep '^go ' "${REPO_ROOT}/go.work" | awk '{print $2}')
if [[ "$go_work_version" == "$oldstable_patch" ]]; then
  ok "go.work declares ${oldstable_patch}"
else
  fail "go.work declares ${go_work_version}, expected ${oldstable_patch}"
fi

# ----- Check all go.mod files -----
printf '\ngo.mod files:\n'
stale_mods=()
total_mods=0

while IFS= read -r -d '' f; do
  rel="${f#"${REPO_ROOT}/"}"
  mod_version=$(grep '^go ' "$f" | awk '{print $2}')
  ((total_mods++)) || true
  if [[ "$mod_version" != "$oldstable_patch" ]]; then
    stale_mods+=("${rel}: ${mod_version}")
  fi
done < <(find "$REPO_ROOT" -name go.mod -print0 | sort -z)

if [[ ${#stale_mods[@]} -eq 0 ]]; then
  ok "${total_mods} go.mod file(s) all declare ${oldstable_patch}"
else
  fail "${#stale_mods[@]} of ${total_mods} go.mod file(s) do not declare ${oldstable_patch}:"
  for m in "${stale_mods[@]}"; do
    printf '    %s\n' "$m"
  done
  printf '  → Run: ./scripts/rollout-go-version.sh\n'
fi

# ----- Check _tools/go.mod (golangci-lint build version) -----
printf '\ngolangci-lint tooling:\n'
tools_mod="${REPO_ROOT}/_tools/go.mod"

if [[ -f "$tools_mod" ]]; then
  tools_go_version=$(grep '^go ' "$tools_mod" | awk '{print $2}')
  if [[ "$tools_go_version" == "$oldstable_patch" ]]; then
    ok "_tools/go.mod declares ${oldstable_patch}"
  else
    warn "_tools/go.mod declares ${tools_go_version} (expected ${oldstable_patch})"
    printf '     golangci-lint is built with Go %s\n' "$tools_go_version"
    printf '     If the new Go release requires a newer golangci-lint, update:\n'
    printf '       1. GOLANGCI_LINT_VERSION in .github/workflows/static-checks.yml\n'
    printf '       2. (cd _tools && go get github.com/golangci/golangci-lint/v2/cmd/golangci-lint)\n'
    printf '       3. (cd _tools && go mod tidy)\n'
  fi
else
  warn "_tools/go.mod not found — skipping golangci-lint check"
fi

# Print current golangci-lint pin for reference
static_checks="${REPO_ROOT}/.github/workflows/static-checks.yml"
if [[ -f "$static_checks" ]]; then
  lint_version_line=$(grep 'GOLANGCI_LINT_VERSION:' "$static_checks" | head -1 || true)
  if [[ -n "$lint_version_line" ]]; then
    lint_version=$(printf '%s' "$lint_version_line" | sed 's/.*GOLANGCI_LINT_VERSION: *\(v[^ #]*\).*/\1/')
    printf '     Current pin: GOLANGCI_LINT_VERSION=%s in static-checks.yml\n' "$lint_version"
  fi
fi

# ----- Summary -----
printf '\nSummary:\n'
printf '  Failures: %d\n' "$failures"
printf '  Warnings: %d\n' "$warnings"
printf '\n'

if [[ $failures -gt 0 ]]; then
  printf 'FAIL — fix the errors above and re-run ./scripts/check-go-versions.sh\n'
  exit 1
elif [[ $warnings -gt 0 ]] && $STRICT; then
  printf 'FAIL (strict mode) — all warnings must be resolved\n'
  exit 1
elif [[ $warnings -gt 0 ]]; then
  printf 'OK (with warnings — run with --strict to treat warnings as failures)\n'
else
  printf 'OK — all Go version checks passed\n'
fi

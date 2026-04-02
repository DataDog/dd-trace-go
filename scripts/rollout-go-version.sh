#!/bin/bash
# rollout-go-version.sh: Update go.work and all go.mod files to match go-versions.yml.
#
# This script reads the stable/oldstable patch versions from go-versions.yml and
# applies them to go.work and every go.mod in the repository. The repo convention
# (documented in go.work) is to pin module files to the oldstable patch version —
# the lowest Go version the library must compile with.
#
# Usage:
#   ./scripts/rollout-go-version.sh            # apply changes
#   ./scripts/rollout-go-version.sh --dry-run  # preview without modifying

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DRY_RUN=false

for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY_RUN=true ;;
    *)
      printf 'Unknown argument: %s\nUsage: %s [--dry-run]\n' "$arg" "$0" >&2
      exit 1
      ;;
  esac
done

VERSIONS_FILE="${REPO_ROOT}/go-versions.yml"
if [[ ! -f "$VERSIONS_FILE" ]]; then
  printf 'ERROR: go-versions.yml not found at %s\n' "$VERSIONS_FILE" >&2
  exit 1
fi

# Parse go-versions.yml (same grep/sed pattern as .github/actions/go-versions/action.yml)
stable=$(grep '^stable:' "$VERSIONS_FILE" | sed 's/.*"\(.*\)".*/\1/')
oldstable=$(grep '^oldstable:' "$VERSIONS_FILE" | sed 's/.*"\(.*\)".*/\1/')
stable_patch=$(grep '^stable_patch:' "$VERSIONS_FILE" | sed 's/.*"\(.*\)".*/\1/')
oldstable_patch=$(grep '^oldstable_patch:' "$VERSIONS_FILE" | sed 's/.*"\(.*\)".*/\1/')

if [[ -z "$stable" || -z "$oldstable" || -z "$stable_patch" || -z "$oldstable_patch" ]]; then
  printf 'ERROR: Failed to parse one or more required fields from go-versions.yml\n' >&2
  exit 1
fi

printf 'Go versions from go-versions.yml:\n'
printf '  stable:    %s (%s)\n' "$stable" "$stable_patch"
printf '  oldstable: %s (%s)\n' "$oldstable" "$oldstable_patch"
printf '\n'
printf 'Target: go.work and all go.mod files will be set to %s\n' "$oldstable_patch"
printf '  (repo convention: pin to lowest supported Go version — see go.work comment)\n'
printf '\n'

if $DRY_RUN; then
  printf '[dry-run] Changes will be listed but not applied.\n\n'
fi

changed=0
mod_updated=0
mod_skipped=0

# ----- Update go.work -----
GO_WORK="${REPO_ROOT}/go.work"
current_work=$(grep '^go ' "$GO_WORK" | awk '{print $2}')

if [[ "$current_work" == "$oldstable_patch" ]]; then
  printf 'go.work: already %s (no change)\n' "$oldstable_patch"
else
  printf 'go.work: %s → %s\n' "$current_work" "$oldstable_patch"
  if ! $DRY_RUN; then
    # Use sed to preserve any inline comment on the go directive line
    sed -i "s/^go [0-9][0-9.]*/go ${oldstable_patch}/" "$GO_WORK"
  fi
  ((changed++)) || true
fi

# ----- Update all go.mod files -----
# Follows the same iteration pattern as scripts/fix_modules.sh
printf '\ngo.mod files:\n'
while IFS= read -r -d '' f; do
  rel="${f#"${REPO_ROOT}/"}"
  current_mod=$(grep '^go ' "$f" | awk '{print $2}')
  if [[ "$current_mod" == "$oldstable_patch" ]]; then
    ((mod_skipped++)) || true
  else
    printf '  %s: %s → %s\n' "$rel" "$current_mod" "$oldstable_patch"
    if ! $DRY_RUN; then
      go mod edit -go="${oldstable_patch}" "$f"
    fi
    ((mod_updated++)) || true
    ((changed++)) || true
  fi
done < <(find "$REPO_ROOT" -name go.mod -print0 | sort -z)

printf '\n  Updated: %d  Already current: %d\n' "$mod_updated" "$mod_skipped"

if ! $DRY_RUN && [[ $mod_updated -gt 0 ]]; then
  printf '\nRunning go mod tidy on all modules...\n'
  while IFS= read -r -d '' f; do
    (
      cd "$(dirname "$f")" || exit 1
      go mod tidy
    )
  done < <(find "$REPO_ROOT" -name go.mod -print0 | sort -z)

  printf 'Updating go.work.sum...\n'
  (cd "$REPO_ROOT" && go list -m all > /dev/null)
fi

printf '\n'
if $DRY_RUN; then
  printf 'Dry run complete. Run without --dry-run to apply changes.\n'
elif [[ $changed -eq 0 ]]; then
  printf 'All files already at %s — nothing to do.\n' "$oldstable_patch"
else
  printf 'Done. %d file(s) updated to Go %s.\n' "$changed" "$oldstable_patch"
  printf 'Next steps:\n'
  printf '  1. Verify:  ./scripts/check-go-versions.sh\n'
  printf '  2. Review:  git diff\n'
  printf '  3. Commit and open a PR\n'
fi

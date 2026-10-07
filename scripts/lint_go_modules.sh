#!/usr/bin/env bash
set -euo pipefail

# Runs golangci-lint in every Go workspace module except the root module.
#
# `golangci-lint run ./...` at the repository root cannot cross Go workspace
# module boundaries, so it never lints the nested modules listed in go.work
# (contrib/*, instrumentation/testutils/*, tools/*, ...). Each of them is linted
# from its own directory instead; golangci-lint finds the root .golangci.yml by
# walking up from there.
#
# Arguments are passed to every `golangci-lint run` call, for example
# --new-from-merge-base=origin/main to report only new issues.
#
# Excluded modules:
#   - the root module: linted by the caller with `golangci-lint run ./...`.
#   - internal/orchestrion/_integration: linted by the caller with its own flags.
#   - .github/workflows/apps: standalone programs run with `go run <file>`; the
#     package declares main several times and does not type-check as a whole.
#   - tools/v2fix/_stage: analyzer test inputs with intentional findings.

usage() {
  cat << EOF
Usage: $(basename "${BASH_SOURCE[0]}") [golangci-lint run flags]

Run golangci-lint in every nested Go workspace module.

Examples:
  $(basename "${BASH_SOURCE[0]}")
  $(basename "${BASH_SOURCE[0]}") --new-from-merge-base=origin/main
EOF
  exit 0
}

if [[ ${1:-} == "-h" || ${1:-} == "--help" ]]; then
  usage
fi

root="$(git rev-parse --show-toplevel)"
cd "$root"

excluded=(
  "$root"
  "$root/internal/orchestrion/_integration"
  "$root/.github/workflows/apps"
  "$root/tools/v2fix/_stage"
)

is_excluded() {
  local dir="$1" e
  for e in "${excluded[@]}"; do
    [[ $dir == "$e" ]] && return 0
  done
  return 1
}

modules="$(go list -m -f '{{.Dir}}')"

failed=()
while IFS= read -r dir; do
  if is_excluded "$dir"; then
    continue
  fi
  rel="${dir#"$root"/}"
  # Some modules only hold files behind build tags (for example tool
  # dependency pins). golangci-lint fails on a module without packages.
  if ! packages="$(cd "$dir" && go list ./...)"; then
    failed+=("$rel")
    continue
  fi
  if [[ -z $packages ]]; then
    printf "\n> Skipping %s: no packages\n" "$rel"
    continue
  fi
  printf "\n> Linting %s\n" "$rel"
  if ! (cd "$dir" && golangci-lint run "$@" ./...); then
    failed+=("$rel")
  fi
done <<< "$modules"

if [[ ${#failed[@]} -gt 0 ]]; then
  printf "\n> golangci-lint failed in %d module(s):\n" "${#failed[@]}"
  printf "  %s\n" "${failed[@]}"
  exit 1
fi

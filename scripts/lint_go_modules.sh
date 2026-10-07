#!/usr/bin/env bash
set -euo pipefail

# Runs golangci-lint in the nested Go workspace modules that have Go changes
# since a git revision, and reports only the issues added since then.
#
# `golangci-lint run ./...` at the repository root stops at Go workspace module
# boundaries, so it never lints the nested modules listed in go.work
# (contrib/*, instrumentation/testutils/*, tools/*, ...). Each changed module is
# linted from its own directory; golangci-lint finds the root .golangci.yml by
# walking up from there.
#
# To avoid repeating work that other checks already do:
#   - Only modules with changed Go files are linted. Issues are limited to the
#     changed lines, so an unchanged module cannot report anything.
#   - Formatters are not run: the check-format job already runs
#     `golangci-lint fmt` on every Go file of the repository.
#
# Excluded modules:
#   - the root module and internal/orchestrion/_integration: the lint job and
#     scripts/lint.sh lint them with their own golangci-lint calls.
#   - .github/workflows/apps: standalone programs run with `go run <file>`; the
#     package declares main several times and does not type-check as a whole.
#   - tools/v2fix/_stage: analyzer test inputs with intentional findings.
#   - orchestrion/all, otelc/all, scripts/fixmodules: their Go files are all
#     behind build tags (tools, scripts), so there is no package to lint.

usage() {
  cat << EOF
Usage: $(basename "${BASH_SOURCE[0]}") <git-revision> [golangci-lint run flags]

Run golangci-lint in the nested Go workspace modules changed since the merge
base of <git-revision> and HEAD, including uncommitted and untracked files.

Examples:
  $(basename "${BASH_SOURCE[0]}") origin/main
  $(basename "${BASH_SOURCE[0]}") HEAD~1 --timeout 10m
EOF
  exit 0
}

if [[ $# -eq 0 || $1 == "-h" || $1 == "--help" ]]; then
  usage
fi
rev="$1"
shift

root="$(git rev-parse --show-toplevel)"
cd "$root"
base="$(git merge-base "$rev" HEAD)"

excluded=(
  "$root"
  "$root/internal/orchestrion/_integration"
  "$root/.github/workflows/apps"
  "$root/tools/v2fix/_stage"
  "$root/orchestrion/all"
  "$root/otelc/all"
  "$root/scripts/fixmodules"
)

is_excluded() {
  local dir="$1" e
  for e in "${excluded[@]}"; do
    [[ $dir == "$e" ]] && return 0
  done
  return 1
}

modules=()
while IFS= read -r dir; do
  modules+=("$dir")
done < <(go list -m -f '{{.Dir}}')
if [[ ${#modules[@]} -eq 0 ]]; then
  echo "go list -m returned no workspace modules" >&2
  exit 1
fi

changed="$(
  git diff --name-only --diff-filter=d "$base" -- '*.go'
  git ls-files --others --exclude-standard -- '*.go'
)"

# A file belongs to the deepest module that contains it: modules nest, for
# example contrib/google.golang.org/api/internal/gen_endpoints.
selected=()
while IFS= read -r file; do
  [[ -z $file ]] && continue
  owner=""
  for dir in "${modules[@]}"; do
    if [[ "$root/$file" == "$dir"/* && ${#dir} -gt ${#owner} ]]; then
      owner="$dir"
    fi
  done
  if [[ -z $owner ]] || is_excluded "$owner"; then
    continue
  fi
  if [[ " ${selected[*]-} " != *" $owner "* ]]; then
    selected+=("$owner")
  fi
done <<< "$changed"

if [[ ${#selected[@]} -eq 0 ]]; then
  printf "> No Go changes in nested modules since %s\n" "$base"
  exit 0
fi

linters="$(golangci-lint linters --color never | awk '/^Enabled by your configuration linters:/ { on = 1; next } /^$/ { on = 0 } on { sub(/:.*/, ""); print }' | paste -sd, -)"
if [[ -z $linters ]]; then
  echo "could not read the enabled linters from golangci-lint linters" >&2
  exit 1
fi

failed=()
for dir in "${selected[@]}"; do
  rel="${dir#"$root"/}"
  printf "\n> Linting %s\n" "$rel"
  if ! (cd "$dir" && golangci-lint run --enable-only="$linters" --new-from-rev="$base" "$@" ./...); then
    failed+=("$rel")
  fi
done

if [[ ${#failed[@]} -gt 0 ]]; then
  printf "\n> golangci-lint failed in %d module(s):\n" "${#failed[@]}"
  printf "  %s\n" "${failed[@]}"
  exit 1
fi

#!/usr/bin/env bash
set -euo pipefail

# Runs testifylint's go-require check on the Go lines that changed since a git
# revision, in every Go workspace module that has Go changes.
#
# Origin of the rule: reviewers repeatedly ask authors to move testify `require`
# calls out of goroutines that are not the test goroutine (httptest handlers,
# `go func()` bodies). require.* calls t.FailNow, which must run on the test
# goroutine: anywhere else a failure Goexits the wrong goroutine, misreports the
# failure, and can panic the binary after the test has completed. The handler
# should record values, and the test goroutine should assert on them.
#
# The check is diff-scoped (--new-from-rev) and testifylint is not in
# `linters.enable` of .golangci.yml, because the full-repo golangci-lint steps
# would then fail on the findings that predate it. This script turns it on with
# --enable-only; .golangci.yml restricts it to the go-require checker.
#
# Like scripts/lint_go_modules.sh, each changed module is linted from its own
# directory, since `golangci-lint run ./...` stops at Go workspace module
# boundaries. Unlike it, this script also covers the root module and
# internal/orchestrion/_integration.
#
# Excluded modules (same reasons as scripts/lint_go_modules.sh):
#   - .github/workflows/apps: its root files do not type-check as a whole.
#   - tools/v2fix/_stage: analyzer test inputs with intentional findings.
#   - orchestrion/all, otelc/all, scripts/fixmodules: every Go file is behind a
#     build tag, so there is no package to lint.

usage() {
  cat << EOF
Usage: $(basename "${BASH_SOURCE[0]}") <git-revision> [golangci-lint run flags]

Run the testifylint go-require check on the lines changed since the merge base
of <git-revision> and HEAD, including uncommitted and untracked files.

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
  printf "> No Go changes since %s\n" "$base"
  exit 0
fi

failed=()
for dir in "${selected[@]}"; do
  rel="${dir#"$root"}"
  rel="${rel#/}"
  printf "\n> testifylint go-require in %s\n" "${rel:-.}"
  if ! (cd "$dir" && golangci-lint run --enable-only=testifylint --new-from-rev="$base" "$@" ./...); then
    failed+=("${rel:-.}")
  fi
done

if [[ ${#failed[@]} -gt 0 ]]; then
  printf "\n> testifylint go-require failed in %d module(s):\n" "${#failed[@]}"
  printf "  %s\n" "${failed[@]}"
  printf "  Do not call require.* off the test goroutine (httptest handlers, go func bodies):\n"
  printf "  record the values there and assert on the test goroutine, or use assert.* / t.Errorf.\n"
  exit 1
fi

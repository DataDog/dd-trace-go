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
# Known limit: go-require only inspects testify require and assert calls. A
# t.Fatal or t.FailNow call off the test goroutine is not flagged, so the same
# review feedback still applies to it by hand. go vet's testinggoroutine
# checker covers the `go func()`-body case; the blind spot is handlers and
# other service goroutines.
#
# The check is diff-scoped (--new-from-rev) and testifylint is not in
# `linters.enable` of .golangci.yml, because the full-repo golangci-lint steps
# would then fail on the findings that predate it. This script turns it on with
# --enable-only; .golangci.yml restricts it to the go-require checker.
#
# Long-term plan: the repo's convention is to fix a linter's backlog first and
# then enable it in `linters.enable` (see the errorlint, loggercheck, and
# unused TODOs there). Once the pre-existing go-require findings are fixed and
# testifylint is enabled, this script can be deleted.
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

module_list="$(go list -m -f '{{.Dir}}')"
# The command substitution above fails the script under set -e when go list
# fails, so a partial module list cannot slip through the empty check below.
if [[ -z $module_list ]]; then
  echo "go list -m failed or returned no workspace modules" >&2
  exit 1
fi
modules=()
while IFS= read -r dir; do
  modules+=("$dir")
done <<< "$module_list"

changed="$(
  git diff --name-only --diff-filter=d "$base" -- '*.go'
  git ls-files --others --exclude-standard -- '*.go'
)"

# A file belongs to the deepest module that contains it: modules nest, for
# example contrib/google.golang.org/api/internal/gen_endpoints.
selected=()
nonws_list=" "
while IFS= read -r file; do
  [[ -z $file ]] && continue
  owner=""
  for dir in "${modules[@]}"; do
    if [[ "$root/$file" == "$dir"/* && ${#dir} -gt ${#owner} ]]; then
      owner="$dir"
    fi
  done
  nonws=false
  if [[ -n $owner ]]; then
    # A go.mod between the file and its workspace module marks a Go module
    # outside go.work. The workspace run's ./... stops at that go.mod, so the
    # file needs a run from its own module directory instead.
    dir="$(dirname "$root/$file")"
    while [[ $dir != "$owner" && $dir != / ]]; do
      if [[ -f "$dir/go.mod" ]]; then
        owner="$dir"
        nonws=true
        break
      fi
      dir="$(dirname "$dir")"
    done
  fi
  if [[ -z $owner ]] || is_excluded "$owner"; then
    continue
  fi
  if [[ " ${selected[*]-} " != *" $owner "* ]]; then
    selected+=("$owner")
    if [[ $nonws == true ]]; then
      nonws_list+="$owner "
    fi
  fi
done <<< "$changed"

# The .github/workflows/apps module is excluded above because its root files
# do not type-check together. Its pr-triage-board subpackage does, so a
# targeted run lints it, the way scripts/lint_go_modules.sh does.
apps_dir=".github/workflows/apps"
lint_pr_triage_board=false
if grep -q "^$apps_dir/pr-triage-board/" <<< "$changed"; then
  lint_pr_triage_board=true
fi

if [[ ${#selected[@]} -eq 0 && $lint_pr_triage_board == false ]]; then
  printf "> No Go changes since %s\n" "$base"
  exit 0
fi

# The caller (scripts/lint_go_modules.sh) lints these modules with testifylint
# included, so a second pass here would repeat the same package loading and
# type-checking. Skip what the caller covers; this script then only lints the
# modules the caller does not reach.
skip="${LINT_TESTIFYLINT_SKIP_MODULES:-}"
if [[ -n $skip ]]; then
  filtered=()
  for dir in "${selected[@]+"${selected[@]}"}"; do
    if [[ $skip != *" $dir "* ]]; then
      filtered+=("$dir")
    fi
  done
  selected=("${filtered[@]+"${filtered[@]}"}")
fi

failed=()
found_require_findings=false
module_out="$(mktemp "${TMPDIR:-/tmp}/testifylint-lint-out.XXXXXX")"
trap 'rm -f "$module_out"' EXIT

for dir in "${selected[@]+"${selected[@]}"}"; do
  rel="${dir#"$root"}"
  rel="${rel#/}"
  printf "\n> testifylint go-require in %s\n" "${rel:-.}"
  # A module outside go.work cannot be linted in workspace mode, because
  # ./... never reaches it. Run it standalone with GOWORK=off instead.
  run_cmd=(golangci-lint)
  if [[ $nonws_list == *" $dir "* ]]; then
    run_cmd=(env GOWORK=off golangci-lint)
  fi
  # tee keeps the findings visible as they print, and the copy lets the
  # summary tell a require.* failure from any other golangci-lint failure.
  if ! (cd "$dir" && "${run_cmd[@]+"${run_cmd[@]}"}" run --enable-only=testifylint --new-from-rev="$base" "$@" ./...) 2>&1 | tee "$module_out"; then
    failed+=("${rel:-.}")
    if grep -q "go-require:" "$module_out"; then
      found_require_findings=true
    fi
  fi
done

if [[ $lint_pr_triage_board == true ]]; then
  printf "\n> testifylint go-require in %s/pr-triage-board\n" "$apps_dir"
  if ! (cd "$root/$apps_dir" && golangci-lint run --enable-only=testifylint --new-from-rev="$base" "$@" ./pr-triage-board) 2>&1 | tee "$module_out"; then
    failed+=("$apps_dir/pr-triage-board")
    if grep -q "go-require:" "$module_out"; then
      found_require_findings=true
    fi
  fi
fi

if [[ ${#failed[@]} -gt 0 ]]; then
  printf "\n> testifylint go-require failed in %d module(s):\n" "${#failed[@]}"
  printf "  %s\n" "${failed[@]}"
  if [[ $found_require_findings == true ]]; then
    printf "  Do not call require.* off the test goroutine (httptest handlers, go func bodies):\n"
    printf "  record the values there and assert on the test goroutine, or use assert.* / t.Errorf.\n"
  else
    printf "  The failure cause is in the golangci-lint output above.\n"
  fi
  exit 1
fi

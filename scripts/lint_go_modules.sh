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
# It also runs scripts/lint_testifylint_new.sh, which applies the testifylint
# go-require check to the added lines of every module, root module included.
# That keeps the check in the CI lint job and in `make lint` without a second
# workflow step.
#
# This script lints every module in go.work unless the list below names it.
# An omitted entry fails loudly when the module has findings. A stale entry
# silently stops the lint. Update the list when a module moves or changes
# purpose.
# Excluded modules:
#   - the root module and internal/orchestrion/_integration: the lint job and
#     scripts/lint.sh lint them with their own golangci-lint calls.
#   - .github/workflows/apps: the loose Go files in the module root are
#     standalone programs run with `go run <file>`; the root package declares
#     main several times and does not type-check as a whole. The
#     pr-triage-board subpackage is a regular package that the targeted run
#     below lints.
#   - tools/v2fix/_stage: analyzer test inputs with intentional findings.
#   - orchestrion/all, otelc/all, scripts/fixmodules: their Go files are all
#     behind build tags (tools, scripts), so there is no package to lint.

usage() {
  cat << EOF
Usage: $(basename "${BASH_SOURCE[0]}") <git-revision> [golangci-lint run flags]

Run golangci-lint in the nested Go workspace modules changed since the merge
base of <git-revision> and HEAD, including uncommitted and untracked files.
It also runs the testifylint go-require check on the changed lines of every
Go module, root module included.

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
# Shared module-discovery helpers. $root must be set before this line.
. "$root/scripts/lib_go_module_discovery.sh"

excluded=(
  "$root"
  "$root/internal/orchestrion/_integration"
  "$root/.github/workflows/apps"
  "$root/tools/v2fix/_stage"
  "$root/orchestrion/all"
  "$root/otelc/all"
  "$root/scripts/fixmodules"
)

module_list="$(lint_go_modules_workspace_dirs)"
# The command substitution above fails the script under set -e when go list
# fails, so a partial module list cannot slip through the empty check below.
if [[ -z $module_list ]]; then
  echo "go list -m failed or returned no workspace modules" >&2
  exit 1
fi

changed="$(lint_go_modules_changed_files "$base")"

# A file belongs to the deepest module that contains it; the rules live in
# lib_go_module_discovery.sh. Modules outside go.work are reported as nonws
# and skipped here: this script lints workspace modules only.
selected=()
selection="$(lint_go_modules_select "$base" "$module_list" "$changed" "${excluded[@]}")"
while IFS=$'\t' read -r owner kind; do
  if [[ -z $owner || $kind != "ws" ]]; then
    continue
  fi
  if [[ " ${selected[*]-} " != *" $owner "* ]]; then
    selected+=("$owner")
  fi
done <<< "$selection"

# The .github/workflows/apps module is excluded above because its root files
# do not type-check together. Its pr-triage-board subpackage does, so a
# targeted run lints it.
apps_dir=".github/workflows/apps"
lint_pr_triage_board=false
if grep -q "^$apps_dir/pr-triage-board/" <<< "$changed"; then
  lint_pr_triage_board=true
fi

# New code must not call require.* off the test goroutine. The check covers the
# root module and internal/orchestrion/_integration, which the rest of this
# script skips, so it must run even when no nested module changed. The check
# runs after the selection above, and skips the modules the loop below lints
# with testifylint included, so each changed nested module gets one lint pass
# instead of two.
testify_rc=0
LINT_TESTIFYLINT_SKIP_MODULES=" ${selected[*]-} " bash "$root/scripts/lint_testifylint_new.sh" "$rev" "$@" || testify_rc=1

if [[ ${#selected[@]} -eq 0 && $lint_pr_triage_board == false ]]; then
  printf "> No Go changes in nested modules since %s\n" "$base"
  exit "$testify_rc"
fi

linters="$(golangci-lint linters --color never | awk '/^Enabled by your configuration linters:/ { on = 1; next } /^$/ { on = 0 } on { sub(/:.*/, ""); print }' | paste -sd, -)"
if [[ -z $linters ]]; then
  echo "could not read the enabled linters from golangci-lint linters" >&2
  exit 1
fi

failed=()
# testifylint rides on the nested-module pass: the go-require settings are in
# .golangci.yml, and the child script skips the modules this loop covers.
nested_linters="$linters,testifylint"
for dir in "${selected[@]+"${selected[@]}"}"; do
  rel="${dir#"$root"/}"
  printf "\n> Linting %s\n" "$rel"
  if ! (cd "$dir" && golangci-lint run --enable-only="$nested_linters" --new-from-rev="$base" "$@" ./...); then
    failed+=("$rel")
  fi
done

if [[ $lint_pr_triage_board == true ]]; then
  printf "\n> Linting %s/pr-triage-board\n" "$apps_dir"
  if ! (cd "$root/$apps_dir" && golangci-lint run --enable-only="$linters" --new-from-rev="$base" "$@" ./pr-triage-board); then
    failed+=("$apps_dir/pr-triage-board")
  fi
fi

if [[ ${#failed[@]} -gt 0 ]]; then
  printf "\n> golangci-lint failed in %d module(s):\n" "${#failed[@]}"
  printf "  %s\n" "${failed[@]}"
  exit 1
fi
exit "$testify_rc"

#!/usr/bin/env bash
# Shared module-discovery helpers for scripts/lint_go_modules.sh and
# scripts/lint_testifylint_new.sh. Source this file; it defines functions
# only. The caller must set $root to the repository top level before use.

# lint_go_modules_workspace_dirs: echoes the directory of every go.work
# module, one per line. Exits non-zero when go list fails. Capture the output
# in a command substitution, so set -e sees the exit status.
lint_go_modules_workspace_dirs() {
  go list -m -f '{{.Dir}}'
}

# lint_go_modules_changed_files <merge-base>: echoes the Go files changed
# since the merge base, plus untracked Go files, one per line. Deleted files
# are left out; renamed files report their destination path.
lint_go_modules_changed_files() {
  git diff --name-only --diff-filter=d "$1" -- '*.go'
  git ls-files --others --exclude-standard -- '*.go'
}

# lint_go_modules_select <merge-base> <module-dirs> <changed-files>
#   <excluded-dir>...: echoes "<dir><TAB>ws" or "<dir><TAB>nonws" for every
# module that owns at least one changed file and is not excluded. A file
# belongs to the deepest module that contains it, because modules nest, for
# example contrib/google.golang.org/api/internal/gen_endpoints. A go.mod
# between the file and its workspace module marks a Go module outside go.work,
# reported as nonws.
lint_go_modules_select() {
  local base="$1" module_dirs="$2" changed_files="$3"
  shift 3
  local file dir owner kind walk excluded_dir
  while IFS= read -r file; do
    [[ -z $file ]] && continue
    owner=""
    while IFS= read -r dir; do
      if [[ "$root/$file" == "$dir"/* && ${#dir} -gt ${#owner} ]]; then
        owner="$dir"
      fi
    done <<< "$module_dirs"
    kind="ws"
    if [[ -n $owner ]]; then
      walk="$(dirname "$root/$file")"
      while [[ $walk != "$owner" && $walk != / ]]; do
        if [[ -f "$walk/go.mod" ]]; then
          owner="$walk"
          kind="nonws"
          break
        fi
        walk="$(dirname "$walk")"
      done
    fi
    if [[ -z $owner ]]; then
      continue
    fi
    for excluded_dir in "$@"; do
      [[ $owner == "$excluded_dir" ]] && continue 2
    done
    printf '%s\t%s\n' "$owner" "$kind"
  done <<< "$changed_files"
}

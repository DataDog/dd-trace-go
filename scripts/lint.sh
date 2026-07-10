#!/usr/bin/env bash
set -euo pipefail

# message: Prints a message to the console with a timestamp and prefix.
message() {
  local msg="$1"
  printf "\n> %s - %s\n" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$msg"
}

# run: Runs the tool and fails early if it fails.
run() {
  local cmd="$1"
  message "Running: $cmd"
  if ! eval "$cmd"; then
    message "Command failed: $cmd"
    exit 1
  fi
  message "Command ran successfully: $cmd"
}

# Default flags
lint_go=false
lint_shell=false
lint_misc=false
lint_action=false

usage() {
  cat << EOF
Usage: $(basename "${BASH_SOURCE[0]}") [options]

Run linters on the codebase.

Options:
  --all          Run all linters and install tools
  --go           Run linters for Go code
  --shell        Run linters for Shell scripts
  --misc         Run miscellaneous linters
  --action       Run linters for GitHub Actions workflows
  -t, --tools    Install linting tools
  -h, --help     Show this help message
EOF
  exit 0
}

lint_go_files() {
  message "Linting Go files..."
  local gopath_bin
  gopath_bin="$(go env GOPATH)/bin"
  export PATH="$gopath_bin:$PATH"
  run "golangci-lint run ./..."
  run "(cd internal/orchestrion/_integration && golangci-lint run --disable=gocritic ./...)"
  run "./scripts/checklocks.sh --ignore-known-issues ./ddtrace/tracer"
}

lint_shell_files() {
  message "Linting shell scripts..."
  run "./scripts/shellcheck.sh"
}

lint_misc_files() {
  message "Running miscellaneous linters..."
  run "go run ./scripts/check_copyright.go"
  run "checkmake --config=.checkmake Makefile scripts/autoreleasetagger/Makefile profiler/internal/fastdelta/Makefile"
}

lint_action_files() {
  message "Linting GitHub Actions workflows..."

  # actionlint (through at least v1.7.12) does not yet understand the GitHub
  # Actions parallel-steps syntax (background/wait/wait-all/cancel/parallel) and
  # rejects any workflow that uses it. Skip files that opt out with the marker
  # below until a supporting release lands, then drop the marker and this
  # handling so the files are linted normally again.
  # Upstream: https://github.com/rhysd/actionlint/pull/694 and pull/695
  local marker="actionlint:skip-file parallel-steps"
  local lint_files=() skipped_files=()
  local file
  for file in .github/workflows/*.yml .github/workflows/*.yaml; do
    [[ -e "${file}" ]] || continue # literal pattern when a glob matches nothing
    if grep -q "${marker}" "${file}"; then
      skipped_files+=("${file}")
    else
      lint_files+=("${file}")
    fi
  done

  if [[ ${#skipped_files[@]} -gt 0 ]]; then
    message "Skipping actionlint for parallel-steps files (unsupported upstream, see rhysd/actionlint#694): ${skipped_files[*]}"
  fi

  if [[ ${#lint_files[@]} -eq 0 ]]; then
    message "No GitHub Actions workflows to lint."
    return
  fi

  # Ignore 'if: false' warnings - used intentionally for disabled jobs like checklocks.
  # The ignore is only available through the command line, not the configuration file.
  run "actionlint -ignore 'condition .false. is always evaluated to false' ${lint_files[*]}"
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
  case $1 in
    --all)
      lint_go=true
      lint_shell=true
      lint_misc=true
      lint_action=true
      shift
      ;;
    --go)
      lint_go=true
      shift
      ;;
    --shell)
      lint_shell=true
      shift
      ;;
    --misc)
      lint_misc=true
      shift
      ;;
    --action)
      lint_action=true
      shift
      ;;
    -h | --help)
      usage
      ;;
    *)
      echo "Ignoring unknown argument $1"
      shift
      ;;
  esac
done

# Default behavior: run linters
if [[ ${lint_go} == false && ${lint_shell} == false && ${lint_misc} == false && ${lint_action} == false ]]; then
  lint_go=true
  lint_shell=true
  lint_misc=true
  lint_action=true
fi

if [[ ${lint_go} == true ]]; then
  lint_go_files
fi

if [[ ${lint_shell} == true ]]; then
  lint_shell_files
fi

if [[ ${lint_misc} == true ]]; then
  lint_misc_files
fi

if [[ ${lint_action} == true ]]; then
  lint_action_files
fi

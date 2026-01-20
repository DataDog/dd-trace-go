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
  run "goimports -e -l -local github.com/DataDog/dd-trace-go/v2 ."
  run "golangci-lint run ./..."
  run "./scripts/checklocks.sh --ignore-known-issues ./ddtrace/tracer"
}

lint_shell_files() {
  message "Linting shell scripts..."
  run "./scripts/shellcheck.sh"
}

lint_misc_files() {
  message "Running miscellaneous linters..."
  run "go run ./scripts/check_copyright.go"
  run "checkmake --config=.checkmake Makefile scripts/autoreleasetagger/Makefile scripts/apiextractor/Makefile profiler/internal/fastdelta/Makefile"
}

lint_action_files() {
  message "Linting GitHub Actions workflows..."
  # Ignore 'if: false' warnings - used intentionally for disabled jobs like checklocks.
  # The ignore is only available through the command line, not the configuration file.
  run "actionlint -ignore 'condition .false. is always evaluated to false'"
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

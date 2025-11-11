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

usage() {
  cat << EOF
Usage: $(basename "${BASH_SOURCE[0]}") [options]

Run linters on the codebase.

Options:
  --all          Run all linters and install tools
  --go           Run linters for Go code
  --shell        Run linters for Shell scripts
  --misc         Run miscellaneous linters
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
  run "./scripts/check_locks.sh --ignore-errors ./ddtrace/tracer"
}

lint_shell_files() {
  message "Linting shell scripts..."
  run "./scripts/shellcheck.sh"
}

lint_misc_files() {
  message "Running miscellaneous linters..."
  run "go run ./scripts/check_copyright.go"
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
  case $1 in
    --all)
      lint_go=true
      lint_shell=true
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
if [[ ${lint_go} == false && ${lint_shell} == false && ${lint_misc} == false ]]; then
  lint_go=true
  lint_shell=true
  lint_misc=true
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

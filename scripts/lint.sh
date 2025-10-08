#!/usr/bin/env bash
set -euo pipefail

# message: Prints a message to the console with a timestamp and prefix.
message() {
  local msg="$1"
  printf "\n> $(date -u +%Y-%m-%dT%H:%M:%SZ) - $msg\n"
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

usage() {
  cat <<EOF
Usage: $(basename "${BASH_SOURCE[0]}") [options]

Run linters on the codebase.

Options:
  --all          Run all linters and install tools
  -t, --tools    Install linting tools
  -h, --help     Show this help message
EOF
  exit 0
}

run_linters() {
  message "Running Linters"
  export PATH="$(go env GOPATH)/bin:$PATH"
  run "goimports -e -l -local github.com/DataDog/dd-trace-go/v2 ."
  run "golangci-lint run ./..."
  run "./scripts/check_locks.sh --ignore-errors ./ddtrace/tracer"
  run "go run ./scripts/check_copyright.go"
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
  case $1 in
  --all)
    run_linters
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
if [[ $# -eq 0 ]]; then
  run_linters
fi

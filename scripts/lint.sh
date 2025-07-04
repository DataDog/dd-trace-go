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

install_tools() {
	message "Installing linting tools..."
	SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &>/dev/null && pwd)
	TEMP_DIR=$(mktemp -d)
	pushd "${TEMP_DIR}"
	go -C "${SCRIPT_DIR}/../_tools" install golang.org/x/tools/cmd/goimports
	go -C "${SCRIPT_DIR}/../_tools" install github.com/golangci/golangci-lint/v2/cmd/golangci-lint
	go -C "${SCRIPT_DIR}/../_tools" install gvisor.dev/gvisor/tools/checklocks/cmd/checklocks@go
	popd
	message "Linting tools installed."
}

run_linters() {
	message "Running Linters"
	export PATH="$(go env GOPATH)/bin:$PATH"
	run "goimports -e -l -local github.com/DataDog/dd-trace-go/v2 ."
	run "golangci-lint run ./..."
	run "./scripts/checklocks.sh --ignore-errors ./ddtrace/tracer"
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
	case $1 in
	--all)
		install_tools
		run_linters
		shift
		;;
	-t | --tools)
		install_tools
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

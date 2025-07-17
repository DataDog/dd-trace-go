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

# Default flags
format_go=false
format_shell=false
tools=false

usage() {
	cat <<EOF
Usage: $(basename "${BASH_SOURCE[0]}") [options]

Format Go and Shell files in the repository.

Options:
  --all          Format both Go and Shell files and install tools
  --go           Format only Go files
  --shell        Format only Shell files
  -t, --tools    Install formatting tools
  -h, --help     Show this help message

Without any flags, formats Go files only (default behavior).
EOF
	exit 0
}

install_tools() {
	message "Installing tools..."
	SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &>/dev/null && pwd)
	TEMP_DIR=$(mktemp -d)
	pushd "${TEMP_DIR}"
	go -C "${SCRIPT_DIR}/../_tools" install github.com/golangci/golangci-lint/v2/cmd/golangci-lint
	go -C "${SCRIPT_DIR}/../_tools" install mvdan.cc/sh/v3/cmd/shfmt
	popd
	message "Tools installed."
}

format_go_files() {
	message "Formatting Go files..."
	run "golangci-lint fmt"
}

format_shell_files() {
	message "Formatting shell scripts..."
	run "shfmt -l -w scripts/*.sh"
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
	case $1 in
	--all)
		install_tools
		format_go=true
		format_shell=true
		shift
		;;
	--go)
		install_tools
		format_go=true
		shift
		;;
	--shell)
		install_tools
		format_shell=true
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

# Default behavior: format Go files if no specific format is selected
if [[ ${format_go} == false ]] && [[ ${format_shell} == false ]]; then
	format_go=true
fi

# Run formatters based on flags
if [[ ${format_go} == true ]]; then
	format_go_files
fi

if [[ ${format_shell} == true ]]; then
	format_shell_files
fi

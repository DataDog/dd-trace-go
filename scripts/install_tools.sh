#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=.github/workflows/apps/go-retry.sh
source "${SCRIPT_DIR}/../.github/workflows/apps/go-retry.sh"

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

usage() {
  cat << EOF
Usage: $(basename "${BASH_SOURCE[0]}") [options]

Install development tools from _tools/tools.go file.

Options:
  -t, --tools-dir DIR     Directory containing tools.go file (default: _tools)
  -b, --bin-dir DIR       Directory to install tools to (default: bin)
  -h, --help              Show this help message

Environment variables:
  GOWORK                  Set to 'off' to disable go.work (default: off)
  GOBIN                   Override binary installation directory

Examples:
  # Install tools to default locations
  ./scripts/install_tools.sh

  # Install tools to custom directory
  ./scripts/install_tools.sh --bin-dir /usr/local/bin

  # Use custom tools directory
  ./scripts/install_tools.sh --tools-dir ./custom-tools --bin-dir ./custom-bin
EOF
  exit 0
}

# Default values
TOOLS_DIR="_tools"
BIN_DIR="bin"
GOWORK="${GOWORK:-off}"

# Parse command line arguments
while [[ $# -gt 0 ]]; do
  case $1 in
    -t | --tools-dir)
      TOOLS_DIR="$2"
      shift 2
      ;;
    -b | --bin-dir)
      BIN_DIR="$2"
      shift 2
      ;;
    -h | --help)
      usage
      ;;
    *)
      echo "Error: Unknown argument $1"
      usage
      ;;
  esac
done

# Validate inputs
if [[ ! -d "$TOOLS_DIR" ]]; then
  echo "Error: Tools directory does not exist: $TOOLS_DIR"
  exit 1
fi

if [[ ! -f "$TOOLS_DIR/tools.go" ]]; then
  echo "Error: tools.go file not found in $TOOLS_DIR"
  exit 1
fi

message "Installing tools from $TOOLS_DIR/tools.go"

# Create bin directory if it doesn't exist
run "mkdir -p $BIN_DIR"

# Get absolute paths
TOOLS_DIR_ABS=$(cd "$TOOLS_DIR" && pwd)
BIN_DIR_ABS=$(cd "$BIN_DIR" && pwd 2> /dev/null || echo "$(pwd)/$BIN_DIR")

# Download dependencies
message "Downloading tool dependencies..."
download_tool_deps() {
  cd "$TOOLS_DIR_ABS" && GOWORK=$GOWORK go mod download
}
if ! retry_on_corruption download_tool_deps; then
  message "Command failed: go mod download"
  exit 1
fi
message "Command ran successfully: go mod download"

# Install tools
message "Installing tools to $BIN_DIR_ABS..."
install_tool_bins() {
  cd "$TOOLS_DIR_ABS" && GOWORK=$GOWORK GOBIN="$BIN_DIR_ABS" go install -v $(grep -E '^[[:space:]]*_[[:space:]]+".*"' tools.go | awk -F'"' '{print $2}')
}
if ! retry_on_corruption install_tool_bins; then
  message "Command failed: go install tools"
  exit 1
fi
message "Command ran successfully: go install tools"

message "Tools installation completed successfully"
message "Installed tools are available in: $BIN_DIR"

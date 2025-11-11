#!/bin/bash

set -e

CHECKLOCKS_PACKAGE="${CHECKLOCKS_PACKAGE:-gvisor.dev/gvisor/tools/checklocks/cmd/checklocks@go}"
CHECKLOCKS_BIN="${CHECKLOCKS_BIN:-}"
IGNORE_ERRORS=false
TARGET_DIR="./ddtrace/tracer"

usage() {
  cat << EOF
Usage: $(basename "${BASH_SOURCE[0]}") [options] [target_directory]

Run checklocks to analyze lock usage and detect potential deadlocks.

Options:
  -i, --ignore-errors    Ignore errors and exit successfully
  -h, --help             Show this help message

Arguments:
  target_directory       Directory to analyze (default: ./ddtrace/tracer)

Environment Variables:
  CHECKLOCKS_PACKAGE     Package to install checklocks from (default: gvisor.dev/gvisor/tools/checklocks/cmd/checklocks@go)
  CHECKLOCKS_BIN         Path to existing checklocks binary (optional)
EOF
  exit 0
}

# Parse arguments
while [[ $# -gt 0 ]]; do
  case $1 in
    --ignore-errors | -i)
      IGNORE_ERRORS=true
      shift
      ;;
    -h | --help)
      usage
      ;;
    *)
      TARGET_DIR="$1"
      shift
      ;;
  esac
done

if [ -n "$CHECKLOCKS_BIN" ]; then
  # Use pre-existing binary if specified
  CHECKLOCKS_PATH="$CHECKLOCKS_BIN"
  echo "Using existing checklocks binary at $CHECKLOCKS_PATH"

  # Verify the binary exists and is executable
  if [ ! -f "$CHECKLOCKS_PATH" ]; then
    echo "Error: Specified checklocks binary does not exist: $CHECKLOCKS_PATH"
    if [ "$IGNORE_ERRORS" = true ]; then
      exit 0
    fi
    exit 1
  fi
  if [ ! -x "$CHECKLOCKS_PATH" ]; then
    echo "Error: Specified checklocks binary is not executable: $CHECKLOCKS_PATH"
    if [ "$IGNORE_ERRORS" = true ]; then
      exit 0
    fi
    exit 1
  fi
else # Check if checklocks tool exists in standard location, install if not
  CHECKLOCKS_PATH="$HOME/go/bin/checklocks"
  if [ ! -f "$CHECKLOCKS_PATH" ]; then
    echo "Installing checklocks tool from $CHECKLOCKS_PACKAGE..."
    pushd /tmp
    go install "$CHECKLOCKS_PACKAGE"
    popd
    echo "checklocks installed at $CHECKLOCKS_PATH"
  fi
fi

echo "Running checklocks on $TARGET_DIR..."

output=$(go vet -vettool="$CHECKLOCKS_PATH" "$TARGET_DIR" 2>&1 || true)
if [ -n "$output" ]; then
  echo "Raw output:"
  echo "$output"

  # Check if all lines start with "-:" or "#"
  ignorable_lines=$(echo "$output" | grep -Evc "^(-:|#)")

  if [ "$ignorable_lines" -eq 0 ]; then
    # All errors start with "-:" or "#", consider it a success
    echo "All errors start with '-:' or '#', considering as success!"
    exit 0
  else
    # Some errors don't start with "-:" or "#", consider it a failure
    echo "Found errors that don't start with '-:' or '#', considering as failure!"
    if [ "$IGNORE_ERRORS" = true ]; then
      echo "Ignoring errors as requested"
      exit 0
    fi
    exit 1
  fi
else
  echo "No errors found"
  exit 0
fi

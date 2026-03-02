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
  -i, --ignore-known-issues    Ignore known issues and exit successfully
  -t, --include-tests          Include test files in the analysis
  -h, --help                   Show this help message

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
    -i | --ignore-known-issues)
      IGNORE_ERRORS=true
      shift
      ;;
    -t | --include-tests)
      INCLUDE_TESTS=true
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

# Conditionally include or exclude test files based on INCLUDE_TESTS flag
if [ "$INCLUDE_TESTS" = true ]; then
  # Include test files - run checklocks directly with default -test=true
  output=$("$CHECKLOCKS_PATH" "$TARGET_DIR" 2>&1 || true)
else
  # Exclude test files - run checklocks directly with -test=false
  output=$("$CHECKLOCKS_PATH" -test=false "$TARGET_DIR" 2>&1 || true)
fi
if [ -n "$output" ]; then
  echo "Raw output:"
  echo "$output"
  echo ""

  # Count total lines in output (excluding empty lines)
  total_lines=$(echo "$output" | grep -c "^" || true)

  # Count ignored errors (lines starting with "-:" or "#")
  ignored_errors=$(echo "$output" | grep -Ec "^(-:|#)" || true)

  # Count suggestions ("may require" lines — often false positives for package-level variables)
  suggestions=$(echo "$output" | grep -c "may require" || true)

  # Count actual errors (excluding ignored lines, suggestions)
  actual_errors=$(echo "$output" | grep -Ev "^(-:|#)" | grep -vc "may require" || true)

  # Print summary
  echo "=========================================="
  echo "Summary:"
  echo "  Total lines:    $total_lines"
  echo "  Actual errors:  $actual_errors"
  echo "  Suggestions:    $suggestions"
  echo "  Ignored errors: $ignored_errors"
  echo "=========================================="

  if [ "$actual_errors" -eq 0 ]; then
    # All errors are ignorable or suggestions, consider it a success
    echo "✓ No hard errors found"
    if [ "$suggestions" -gt 0 ]; then
      echo "  (${suggestions} suggestion(s) — review manually)"
    fi
    exit 0
  else
    # Some errors are hard errors, consider it a failure
    echo "✗ Found $actual_errors actual error(s)"
    if [ "$IGNORE_ERRORS" = true ]; then
      echo "Ignoring errors as requested"
      exit 0
    fi
    exit 1
  fi
else
  echo "=========================================="
  echo "Summary:"
  echo "  ✓ No errors found"
  echo "=========================================="
  exit 0
fi

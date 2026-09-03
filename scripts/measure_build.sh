#!/usr/bin/env bash
# Unless explicitly stated otherwise all files in this repository are licensed
# under the Apache License Version 2.0.
# This product includes software developed at Datadog (https://www.datadoghq.com/).
# Copyright 2026 Datadog, Inc.
set -euo pipefail

# measure_build.sh — measure build time and binary size for Orchestrion integration samples
#
# Usage: scripts/measure_build.sh [OPTIONS]
#
# Options:
#   --sample NAME         Sample to build (default: net_http)
#   --mode MODE           Build mode: standard or orchestrion (required)
#   --output PATH         Output JSON file path (default: stdout)
#   --repeats N           Number of build repeats (default: 3)
#   -h, --help            Show this help message
#
# Output JSON includes all build_duration_samples (one per repeat) and a single
# binary_size_bytes taken from the last build. In standard mode, if `gsa`
# (go-size-analyzer) is on PATH, the JSON also includes dependency_sizes: the
# top 10 vendor packages by size. The JSON also includes
# dependency_total_size_bytes and dependency_count: the summed size and the
# count of every vendor package, including packages outside the top 10.
# These two fields attribute the binary size to specific dependencies and to
# overall dependency bloat.
#
# Examples:
#   scripts/measure_build.sh --sample net_http --mode standard
#   scripts/measure_build.sh --sample net_http --mode orchestrion --output /tmp/metrics.json

usage() {
  cat << EOF
Usage: $(basename "${BASH_SOURCE[0]}") [OPTIONS]

Measure build time and binary size for Orchestrion integration samples.
Builds are performed with a cold build cache to measure full compilation cost.

Options:
  --sample NAME         Sample to build (default: net_http)
  --mode MODE           Build mode: standard or orchestrion (required)
  --output PATH         Output JSON file path (default: stdout)
  --repeats N           Number of build repeats (default: 3)
  -h, --help            Show this help message

Examples:
  $(basename "${BASH_SOURCE[0]}") --sample net_http --mode standard
  $(basename "${BASH_SOURCE[0]}") --sample net_http --mode orchestrion --output /tmp/metrics.json
EOF
  exit 0
}

message() {
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*" >&2
}

die() {
  message "ERROR: $*"
  exit 1
}

# Defaults
SAMPLE="net_http"
MODE=""
OUTPUT=""
REPEATS=3

# Parse arguments
while [[ $# -gt 0 ]]; do
  case "$1" in
    --sample)
      SAMPLE="$2"
      shift 2
      ;;
    --mode)
      MODE="$2"
      shift 2
      ;;
    --output)
      OUTPUT="$2"
      shift 2
      ;;
    --repeats)
      REPEATS="$2"
      shift 2
      ;;
    -h | --help)
      usage
      ;;
    *)
      die "Unknown argument: $1"
      ;;
  esac
done

# Validate required arguments
if [[ -z "$MODE" ]]; then
  die "--mode is required (standard or orchestrion)"
fi

if [[ "$MODE" != "standard" && "$MODE" != "orchestrion" ]]; then
  die "--mode must be 'standard' or 'orchestrion', got: $MODE"
fi

# Find repo root
REPO_ROOT="$(git rev-parse --show-toplevel)"
INTEGRATION_DIR="$REPO_ROOT/internal/orchestrion/_integration"

# Validate sample exists
if [[ ! -d "$INTEGRATION_DIR/$SAMPLE" ]]; then
  die "Sample directory not found: $INTEGRATION_DIR/$SAMPLE"
fi

# Output directory for binaries
OUT_DIR="$(mktemp -d)"
trap 'rm -rf "$OUT_DIR"' EXIT

message "Build configuration:"
message "  Sample: $SAMPLE"
message "  Mode: $MODE"
message "  Repeats: $REPEATS"
message "  Integration dir: $INTEGRATION_DIR"
message "  Output dir: $OUT_DIR"

cd "$INTEGRATION_DIR" || die "Failed to cd to $INTEGRATION_DIR"

# Warm module cache (untimed)
message "Warming module download cache..."
go mod download || die "go mod download failed"

# For orchestrion mode, ensure the binary is installed (untimed)
if [[ "$MODE" == "orchestrion" ]]; then
  message "Installing orchestrion binary..."
  go install "github.com/DataDog/orchestrion" || die "Failed to install orchestrion"
  ORCHESTRION_VERSION="$(go list -m -f '{{.Version}}' github.com/DataDog/orchestrion)"
  message "  Orchestrion version: $ORCHESTRION_VERSION"
fi

# Get Go version
GO_VERSION="$(go version | awk '{print $3}' | sed 's/go//')"
message "  Go version: $GO_VERSION"

# Build function
do_build() {
  local bin_path="$OUT_DIR/$SAMPLE-$MODE.test"

  # Cold build cache
  message "Cleaning build cache..."
  go clean -cache

  # Timed build
  message "Building $SAMPLE with $MODE toolchain..."
  local start_time
  start_time=$(date +%s.%N 2> /dev/null || date +%s)

  if [[ "$MODE" == "standard" ]]; then
    go test -c -o "$bin_path" "./$SAMPLE" || die "Build failed (standard)"
  else
    go test -c -toolexec='orchestrion toolexec' -o "$bin_path" "./$SAMPLE" || die "Build failed (orchestrion)"
  fi

  local end_time
  end_time=$(date +%s.%N 2> /dev/null || date +%s)
  local duration
  duration=$(awk "BEGIN {print $end_time - $start_time}")

  # Binary size
  local size
  size=$(stat -c %s "$bin_path" 2> /dev/null || stat -f %z "$bin_path" 2> /dev/null) || die "Failed to stat binary"

  message "  Duration: ${duration}s"
  message "  Size: $size bytes"

  echo "$duration $size"
}

# Perform builds — collect all duration samples; use last build's binary size
message "Performing $REPEATS builds..."
durations=()
size=""
for i in $(seq 1 "$REPEATS"); do
  message "Build $i/$REPEATS:"
  read -r d s <<< "$(do_build)"
  durations+=("$d")
  size="$s"
done
message "Durations: ${durations[*]}, size: $size bytes"

# Dependency size attribution (standard mode only — orchestrion mode builds the
# same source, so re-running the analysis there would just duplicate this data)
DEPENDENCY_SIZES="[]"
DEPENDENCY_TOTAL_SIZE_BYTES=0
DEPENDENCY_COUNT=0
if [[ "$MODE" == "standard" ]] && command -v gsa &> /dev/null; then
  message "Attributing binary size to dependencies with gsa..."
  bin_path="$OUT_DIR/$SAMPLE-$MODE.test"
  gsa_json="$OUT_DIR/gsa.json"
  if gsa "$bin_path" -f json --compact --no-disasm -o "$gsa_json"; then
    # Filter vendor packages once. Reuse the result for the top-10 slice and the totals below.
    VENDOR_PACKAGES=$(jq '[.packages | to_entries[] | select(.value.type == "vendor")]' "$gsa_json")
    DEPENDENCY_SIZES=$(jq '[
      sort_by(-.value.size)
      | .[0:10]
      | .[]
      | { name: .key, metric_key: (.key | ascii_downcase | gsub("[^a-z0-9_]+"; "_")), size_bytes: .value.size }
    ]' <<< "$VENDOR_PACKAGES")
    DEPENDENCY_TOTAL_SIZE_BYTES=$(jq '[.[].value.size] | add // 0' <<< "$VENDOR_PACKAGES")
    DEPENDENCY_COUNT=$(jq 'length' <<< "$VENDOR_PACKAGES")
    message "  Dependencies: $DEPENDENCY_COUNT, total size: $DEPENDENCY_TOTAL_SIZE_BYTES bytes"
  else
    message "  gsa analysis failed; continuing without dependency_sizes"
  fi
else
  message "Skipping dependency attribution (gsa not found or mode is orchestrion)"
fi

# Build JSON output — durations as array, size as single value
message "Generating JSON output..."
DURATION_ARRAY=$(printf '%s\n' "${durations[@]}" | jq -R 'tonumber' | jq -s '.')
JSON=$(jq -n \
  --arg sample "$SAMPLE" \
  --arg mode "$MODE" \
  --argjson durations "$DURATION_ARRAY" \
  --argjson size "$size" \
  --argjson dependency_sizes "$DEPENDENCY_SIZES" \
  --argjson dependency_total_size_bytes "$DEPENDENCY_TOTAL_SIZE_BYTES" \
  --argjson dependency_count "$DEPENDENCY_COUNT" \
  --arg go_version "$GO_VERSION" \
  '{ sample: $sample, mode: $mode, metrics: { build_duration_samples: $durations, binary_size_bytes: $size, dependency_sizes: $dependency_sizes, dependency_total_size_bytes: $dependency_total_size_bytes, dependency_count: $dependency_count }, go_version: $go_version }')

# Add orchestrion version if in orchestrion mode
if [[ "$MODE" == "orchestrion" ]]; then
  JSON=$(echo "$JSON" | jq --arg orch_version "$ORCHESTRION_VERSION" '. + {orchestrion_version: $orch_version}')
fi

# Output
if [[ -z "$OUTPUT" ]]; then
  echo "$JSON"
else
  echo "$JSON" > "$OUTPUT"
  message "Wrote JSON to $OUTPUT"
fi

message "Done."

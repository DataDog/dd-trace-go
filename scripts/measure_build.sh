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
#   --mode MODE           Build mode: standard, orchestrion, or otelc (required)
#   --output PATH         Output JSON file path (default: stdout)
#   --repeats N           Number of build repeats (default: 3)
#   -h, --help            Show this help message
#
# Output JSON includes all build_duration_samples (one per repeat) and a single
# binary_size_bytes taken from the last build.
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
  --mode MODE           Build mode: standard, orchestrion, or otelc (required)
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
  die "--mode is required (standard, orchestrion, otelc)"
fi

if [[ "$MODE" != "standard" && "$MODE" != "orchestrion" && "$MODE" != "otelc" ]]; then
  die "--mode must be 'standard', 'orchestrion', or 'otelc', got: $MODE"
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

if [[ "$MODE" == "otelc" ]]; then
  message "Cloning and installing otelc binary..."
  OTELC_SRC_DIR="$OUT_DIR/otelc-src"
  OTELC_REF="3b9beed7a1200cf16e59bafa431dd4c4a0601f41" # v1.0.1
  mkdir -p "$OTELC_SRC_DIR"
  git -C "$OTELC_SRC_DIR" init -q
  git -C "$OTELC_SRC_DIR" remote add origin https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation.git
  git -C "$OTELC_SRC_DIR" fetch --depth 1 origin "$OTELC_REF" || die "Failed to fetch otelc commit $OTELC_REF"
  git -C "$OTELC_SRC_DIR" checkout -q FETCH_HEAD || die "Failed to checkout otelc commit $OTELC_REF"
  (cd "$OTELC_SRC_DIR" && make install) || die "Failed to install otelc"
  OTELC_VERSION="$OTELC_REF"
  message "  Otelc version: $OTELC_VERSION"
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
  elif [[ "$MODE" == "orchestrion" ]]; then
    go test -c -toolexec='orchestrion toolexec' -o "$bin_path" "./$SAMPLE" || die "Build failed (orchestrion)"
  elif [[ "$MODE" == "otelc" ]]; then
    otelc -rules="$REPO_ROOT" go test -c -o "$bin_path" "./$SAMPLE" || die "Build failed (otelc)"
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

# Build JSON output — durations as array, size as single value
message "Generating JSON output..."
DURATION_ARRAY=$(printf '%s\n' "${durations[@]}" | jq -R 'tonumber' | jq -s '.')
JSON=$(jq -n \
  --arg sample "$SAMPLE" \
  --arg mode "$MODE" \
  --argjson durations "$DURATION_ARRAY" \
  --argjson size "$size" \
  --arg go_version "$GO_VERSION" \
  '{ sample: $sample, mode: $mode, metrics: { build_duration_samples: $durations, binary_size_bytes: $size }, go_version: $go_version }')

# Add orchestrion version if in orchestrion mode
if [[ "$MODE" == "orchestrion" ]]; then
  JSON=$(echo "$JSON" | jq --arg orch_version "$ORCHESTRION_VERSION" '. + {orchestrion_version: $orch_version}')
fi

# Add otelc version if in otelc mode
if [[ "$MODE" == "otelc" ]]; then
  JSON=$(echo "$JSON" | jq --arg otelc_version "$OTELC_VERSION" '. + {otelc_version: $otelc_version}')
fi

# Output
if [[ -z "$OUTPUT" ]]; then
  echo "$JSON"
else
  echo "$JSON" > "$OUTPUT"
  message "Wrote JSON to $OUTPUT"
fi

message "Done."

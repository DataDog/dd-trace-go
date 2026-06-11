#!/usr/bin/env bash
# Unless explicitly stated otherwise all files in this repository are licensed
# under the Apache License Version 2.0.
# This product includes software developed at Datadog (https://www.datadoghq.com/).
# Copyright 2026 Datadog, Inc.
set -euo pipefail

# publish_build_metrics.sh — publish build metrics to Datadog CI Visibility
#
# Reads build metrics JSON (from measure_build.sh) and publishes them as
# custom measures and tags on the current CI job span using datadog-ci.
#
# Environment variables:
#   METRICS_FILE        Path to metrics JSON file (required)
#   DATADOG_API_KEY     Datadog API key (required)
#   DATADOG_SITE        Datadog site (default: datadoghq.com)
#
# Usage: scripts/publish_build_metrics.sh

message() {
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*" >&2
}

die() {
  message "ERROR: $*"
  exit 1
}

# Validate environment
if [[ -z "${METRICS_FILE:-}" ]]; then
  die "METRICS_FILE environment variable is required"
fi

if [[ -z "${DATADOG_API_KEY:-}" ]]; then
  die "DATADOG_API_KEY environment variable is required"
fi

if [[ ! -f "$METRICS_FILE" ]]; then
  die "Metrics file not found: $METRICS_FILE"
fi

# Parse JSON
message "Reading metrics from $METRICS_FILE"
SAMPLE=$(jq -r '.sample' "$METRICS_FILE")
MODE=$(jq -r '.mode' "$METRICS_FILE")
SIZE=$(jq -r '.metrics.binary_size_bytes' "$METRICS_FILE")
GO_VERSION=$(jq -r '.go_version' "$METRICS_FILE")
ORCHESTRION_VERSION=$(jq -r '.orchestrion_version // empty' "$METRICS_FILE")

# Read all duration samples into a bash array
mapfile -t DURATIONS < <(jq -r '.metrics.build_duration_samples[]' "$METRICS_FILE")

message "Parsed metrics:"
message "  Sample: $SAMPLE"
message "  Mode: $MODE"
message "  Durations: ${DURATIONS[*]}s"
message "  Size: $SIZE bytes"
message "  Go version: $GO_VERSION"
if [[ -n "$ORCHESTRION_VERSION" ]]; then
  message "  Orchestrion version: $ORCHESTRION_VERSION"
fi

# Publish measures to CI Visibility — one indexed measure per duration sample, one size sample
message "Publishing measures to Datadog CI Visibility..."
MEASURE_ARGS=(--measures "go.build.binary_size_bytes:${SIZE}")
for i in "${!DURATIONS[@]}"; do
  MEASURE_ARGS+=(--measures "go.build.duration_seconds.${i}:${DURATIONS[$i]}")
done

DATADOG_SITE="${DATADOG_SITE:-datadoghq.com}" datadog-ci measure --level job \
  "${MEASURE_ARGS[@]}" ||
  die "Failed to publish measures"

# Publish tags
message "Publishing tags to Datadog CI Visibility..."
TAGS=(
  "build.toolchain:${MODE}"
  "build.sample:${SAMPLE}"
  "build.cache:cold"
  "go.version:${GO_VERSION}"
)

if [[ -n "$ORCHESTRION_VERSION" ]]; then
  TAGS+=("orchestrion.version:${ORCHESTRION_VERSION}")
fi

# Build tag arguments
TAG_ARGS=()
for tag in "${TAGS[@]}"; do
  TAG_ARGS+=(--tags "$tag")
done

DATADOG_SITE="${DATADOG_SITE:-datadoghq.com}" datadog-ci tag --level job "${TAG_ARGS[@]}" ||
  die "Failed to publish tags"

message "Successfully published metrics and tags"

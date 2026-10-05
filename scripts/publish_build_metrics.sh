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
#   METRICS_FILE         Path to metrics JSON file (required)
#   DATADOG_API_KEY      Datadog API key (required)
#   DATADOG_SITE         Datadog site (default: datadoghq.com)
# Cache utilization flags (optional; prefixes GO_MOD_, GSA_, DATADOG_CI_):
#   <prefix>CACHE_OUTCOME  outcome of this job's actions/cache step ('skipped'
#                          or empty means the step did not run)
#   <prefix>CACHE_HIT      its cache-hit output: 'true' exact match, 'false'
#                          restore-key match, empty on a miss
#
# Each cache whose step ran is published as a ci.cache.<name>.hit measure
# (1 = exact hit, no network; 0 = miss or partial restore) so utilization
# can be tracked in CI Visibility.
#
# Usage: scripts/publish_build_metrics.sh

message() {
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*" >&2
}

die() {
  message "ERROR: $*"
  exit 1
}

mean() {
  awk 'BEGIN { s = 0 } { for (i = 1; i <= NF; i++) s += $i } END { print s / NF }' <<< "$*"
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

# Read dependency size attribution (standard mode only; empty otherwise).
# Sorted descending by size (see measure_build.sh), so index 0 is the single
# largest dependency, index 1 the second largest, etc.
mapfile -t DEP_NAMES < <(jq -r '.metrics.dependency_sizes[]?.name' "$METRICS_FILE")
mapfile -t DEP_KEYS < <(jq -r '.metrics.dependency_sizes[]?.metric_key' "$METRICS_FILE")
mapfile -t DEP_SIZES < <(jq -r '.metrics.dependency_sizes[]?.size_bytes' "$METRICS_FILE")

message "Parsed metrics:"
message "  Sample: $SAMPLE"
message "  Mode: $MODE"
message "  Durations: ${DURATIONS[*]}s"
message "  Size: $SIZE bytes"
message "  Dependencies attributed: ${#DEP_KEYS[@]}"
message "  Go version: $GO_VERSION"
if [[ -n "$ORCHESTRION_VERSION" ]]; then
  message "  Orchestrion version: $ORCHESTRION_VERSION"
fi

# Publish measures to CI Visibility — one indexed measure per duration
# sample plus a flat mean duration measure (repeated samples of the same
# build, so a mean is meaningful), and (standard mode only) one measure per
# dependency attributed by gsa, named after the dependency for historical,
# per-dependency trend queries.
message "Publishing measures to Datadog CI Visibility..."
MEAN_DURATION=$(mean "${DURATIONS[@]}")
message "  Mean duration: ${MEAN_DURATION}s"
MEASURE_ARGS=(
  --measures "go.build.binary_size_bytes:${SIZE}"
  --measures "go.build.duration_seconds:${MEAN_DURATION}"
)
for i in "${!DURATIONS[@]}"; do
  MEASURE_ARGS+=(--measures "go.build.duration_seconds.${i}:${DURATIONS[$i]}")
done
for i in "${!DEP_KEYS[@]}"; do
  MEASURE_ARGS+=(--measures "go.build.dependency_size_bytes.${DEP_KEYS[$i]}:${DEP_SIZES[$i]}")
  # Publish dependency size bytes by rank (0 = single largest dependency in this build)
  MEASURE_ARGS+=(--measures "go.build.top_dependency_size_bytes.${i}:${DEP_SIZES[$i]}")
done

# Cache utilization: 1 = exact actions/cache hit (no network), 0 = miss or
# partial restore (network needed). actions/cache emits 'true' only on an
# exact match, 'false' on a restore-key match, and an empty string on a miss,
# so the workflow passes the cache step's outcome alongside to tell a miss
# apart from a cache step that did not run (gsa in orchestrion mode): only a
# cache whose step ran publishes a measure.
publish_cache_measure() {
  # $1: measure suffix, $2: step outcome, $3: cache-hit output
  local hit
  [[ -z "$2" || "$2" == "skipped" ]] && return 0
  if [[ "$3" == "true" ]]; then hit=1; else hit=0; fi
  MEASURE_ARGS+=(--measures "ci.cache.${1}.hit:${hit}")
  CACHE_REPORTS+=("${1}=${hit}")
}

CACHE_REPORTS=()
publish_cache_measure go_modules "${GO_MOD_CACHE_OUTCOME:-}" "${GO_MOD_CACHE_HIT:-}"
publish_cache_measure gsa "${GSA_CACHE_OUTCOME:-}" "${GSA_CACHE_HIT:-}"
publish_cache_measure datadog_ci "${DATADOG_CI_CACHE_OUTCOME:-}" "${DATADOG_CI_CACHE_HIT:-}"
message "  Cache utilization: ${CACHE_REPORTS[*]:-none reported}"

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

for i in "${!DEP_NAMES[@]}"; do
  TAGS+=("build.top_dependency_name.${i}:${DEP_NAMES[$i]}")
done

# Build tag arguments
TAG_ARGS=()
for tag in "${TAGS[@]}"; do
  TAG_ARGS+=(--tags "$tag")
done

DATADOG_SITE="${DATADOG_SITE:-datadoghq.com}" datadog-ci tag --level job "${TAG_ARGS[@]}" ||
  die "Failed to publish tags"

message "Successfully published metrics and tags"

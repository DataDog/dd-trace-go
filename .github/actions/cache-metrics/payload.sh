#!/usr/bin/env bash
# Builds the Datadog v2 series payload for .github/actions/cache-metrics and
# prints it to stdout. The action submits it; this script only computes it, so
# scripts/actiontest can run it on simulated observations.
#
# Environment: CACHE_METRICS, MEASURE_SIZES, GITHUB_REPOSITORY, CI_JOB_NAME,
# CI_PIPELINE_NAME, CI_TRIGGER, RUNNER_OS, RUNNER_ARCH.
set -euo pipefail

normalize_tag() {
  printf '%s' "$1" | tr '[:upper:]' '[:lower:]' | sed -E -e 's/[^a-z0-9_.-]+/_/g' -e 's/^_//' -e 's/_$//'
}

size_bytes() {
  local path="$1" kb
  if command -v cygpath >/dev/null 2>&1; then
    path="$(cygpath -u "$path")"
  fi
  [[ -d "$path" ]] || return 0
  kb="$(du -sk "$path" 2>/dev/null | awk 'NR == 1 {print $1}')"
  [[ "$kb" =~ ^[0-9]+$ ]] || return 0
  printf '%s' "$((kb * 1024))"
}

sizes=""
if [[ "${MEASURE_SIZES:-false}" == "true" ]]; then
  while IFS= read -r -d '' name && IFS= read -r -d '' path; do
    size="$(size_bytes "$path")"
    [[ -n "$size" ]] && sizes+="${name}"$'\t'"${size}"$'\n'
  done < <(jq -j '.cache_paths[] | select(.path != null and .path != "") | .name, "\u0000", .path, "\u0000"' <<< "$CACHE_METRICS")
fi

provider_raw="$(jq -r '.provider // empty' <<<"$CACHE_METRICS")"
workload_raw="$(jq -r '.workload // empty' <<<"$CACHE_METRICS")"
provider_tag="$(normalize_tag "${provider_raw:-github-cache}")"
workload_tag="$(normalize_tag "${workload_raw:-unknown}")"
tags="$(jq -cn \
  --arg repository "$(normalize_tag "${GITHUB_REPOSITORY##*/}")" \
  --arg os_platform "$(normalize_tag "${RUNNER_OS:-unknown}")" \
  --arg os_architecture "$(normalize_tag "${RUNNER_ARCH:-unknown}")" \
  --arg pipeline "$(normalize_tag "${CI_PIPELINE_NAME:-unknown}")" \
  --arg job "$(normalize_tag "${CI_JOB_NAME:-unknown}")" \
  --arg trigger "$(normalize_tag "${CI_TRIGGER:-unknown}")" \
  --arg provider "$provider_tag" \
  --arg workload "$workload_tag" \
  '["repository:" + (if $repository == ""
    then "unknown" else $repository end),
    "os.platform:" + $os_platform,
    "os.architecture:" + $os_architecture,
    "ci.pipeline.name:" + $pipeline,
    "ci.job.name:" + $job,
    "ci.trigger:" + $trigger,
    "provider:" + $provider,
    "workload:" + $workload]'
)"

# A cache_matched_key field selects exact/partial/miss classification.
# Observations without that field use aggregate hit/miss classification.
# For the github-cache provider a `false` cache-hit is a cold miss:
# actions/setup-go restores with the primary key only. For the cloudx
# provider a `false` cache-hit is ambiguous between a prefix restore and
# a cold miss, so the setup-go action adds a `restored` field
# ("true" when the module cache is non-empty after the restore). With it,
# a non-exact restore reports hit/partial when `restored` is "true" and
# miss/miss when it is "false"; without it the result stays miss/miss, the
# contract this series has always had, where `miss` means "not an exact
# hit". The offline collector in scripts/citiming applies the same rule,
# reporting an absent `restored` as unknown.
jq -cn \
  --argjson metrics "$CACHE_METRICS" \
  --argjson timestamp "$(date +%s)" \
  --argjson tags "$tags" \
  --arg sizes "$sizes" '
  def runtime_tags:
    ["runtime.name:" + $metrics.runtime.name,
     "runtime.version:" + (if $metrics.runtime.version == ""
     then "unknown" else $metrics.runtime.version end)];
  def count_series($name; $result):
    {metric: "ci.step.cache.restore", type: 1, interval: 1,
     points: [{timestamp: $timestamp, value: 1}],
     tags: ($tags + runtime_tags + ["cache_name:" + $name,
            "outcome:" + $result[0], "hit_type:" + $result[1]])};
  def size_series($measurement):
    {metric: "ci.cache.disk_size_bytes", type: 3,
     points: [{timestamp: $timestamp,
              value: ($measurement[1] | tonumber)}],
     tags: ($tags + runtime_tags + ["cache_name:" + $measurement[0],
            "phase:end_of_job"])};
  def restore_result:
    if .enabled != "true" or .outcome != "success" then empty
    elif $metrics.provider == "cloudx"
         and (has("cache_matched_key") | not) then
      if .cache_hit == "true" then ["hit", "exact"]
      elif .restored == "true" then ["hit", "partial"]
      else ["miss", "miss"] end
    elif has("cache_matched_key") then
      if .cache_hit == "true" then ["hit", "exact"]
      elif .cache_hit == "false" or .cache_hit == "" then
        if .cache_matched_key != ""
        then ["hit", "partial"] else ["miss", "miss"] end
      else empty
      end
    elif .cache_hit == "true" then ["hit", "unknown"]
    elif .cache_hit == "false" then ["miss", "miss"]
    else empty
    end;
  {series: ([$metrics.restores[] as $restore |
              $restore | restore_result
              | count_series($restore.name; .)] +
            [$sizes | split("\n")[] | select(length > 0) |
              split("\t") | size_series(.)])}
'

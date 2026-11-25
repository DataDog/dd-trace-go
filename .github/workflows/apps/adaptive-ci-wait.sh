#!/usr/bin/env bash
# Unless explicitly stated otherwise all files in this repository are licensed
# under the Apache License Version 2.0.
# This product includes software developed at Datadog (https://www.datadoghq.com/).
# Copyright 2026 Datadog, Inc.

set -euo pipefail

MIN_WAIT_SECONDS="${MIN_WAIT_SECONDS:-300}"
MAX_TOTAL_WAIT_SECONDS="${MAX_TOTAL_WAIT_SECONDS:-10800}"
MAX_INITIAL_WAIT_SECONDS="${MAX_INITIAL_WAIT_SECONDS:-5400}"
TOP_RUNS_COUNT="${TOP_RUNS_COUNT:-10}"
SLEEP_CMD="${SLEEP_CMD:-sleep}"
CURRENT_JOB_NAME="${CURRENT_JOB_NAME:-all-jobs-are-green}"
IGNORED_NAME_PATTERNS="${IGNORED_NAME_PATTERNS:-}"

write_output() {
  local next_delay="$1"
  local remaining_retries="$2"
  local polling_interval="$3"

  if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
    {
      echo "next-delay-seconds=${next_delay}"
      echo "remaining-retries=${remaining_retries}"
      echo "polling-interval-seconds=${polling_interval}"
    } >> "$GITHUB_OUTPUT"
  else
    echo "next-delay-seconds=${next_delay}"
    echo "remaining-retries=${remaining_retries}"
    echo "polling-interval-seconds=${polling_interval}"
  fi
}

compute_remaining_retries() {
  local elapsed="$1"
  local remaining_seconds=$((MAX_TOTAL_WAIT_SECONDS - elapsed))
  if [[ "$remaining_seconds" -le 0 ]]; then
    echo "1"
    return 0
  fi

  local retries=$(((remaining_seconds + MIN_WAIT_SECONDS - 1) / MIN_WAIT_SECONDS))
  if [[ "$retries" -lt 1 ]]; then
    retries=1
  fi
  echo "$retries"
}

# Fetch recent successful pull_request workflow runs and return the average
# duration in seconds of the top N longest runs, excluding this workflow.
compute_avg_longest_seconds() {
  local runs_json
  if ! runs_json="$(gh api "repos/${GITHUB_REPOSITORY}/actions/runs?event=pull_request&status=success&per_page=100")"; then
    echo "Warning: failed to query workflow runs; falling back to minimum wait." >&2
    echo "0"
    return 0
  fi

  if ! jq -r --argjson top_n "$TOP_RUNS_COUNT" '
    (.workflow_runs // [])
    | map(select(.path != ".github/workflows/all-green.yml"))
    | map(select(.run_started_at != null and .updated_at != null))
    | map((.updated_at | fromdateiso8601) - (.run_started_at | fromdateiso8601))
    | map(select(. > 0))
    | sort_by(-.)
    | .[:$top_n]
    | if length == 0 then 0 else (add / length | floor) end
  ' <<< "$runs_json"; then
    echo "Warning: failed to parse workflow runs; falling back to minimum wait." >&2
    echo "0"
  fi
}

# Return "done" when all non-ignored checks have finished or any has failed.
# Return "pending" when checks are still running or when status cannot be read.
check_ci_status() {
  local check_runs_json status_json

  if ! check_runs_json="$(gh api --paginate "repos/${GITHUB_REPOSITORY}/commits/${TARGET_SHA}/check-runs?per_page=100" | jq -s '[.[].check_runs[]? | {name, status, conclusion}]')"; then
    echo "Warning: failed to fetch check runs; treating checks as pending." >&2
    echo "pending"
    return 0
  fi

  if ! status_json="$(gh api --paginate "repos/${GITHUB_REPOSITORY}/commits/${TARGET_SHA}/status?per_page=100" | jq -s '[.[].statuses[]? | {context, state}]')"; then
    echo "Warning: failed to fetch commit statuses; treating checks as pending." >&2
    echo "pending"
    return 0
  fi

  if ! printf '%s\n%s\n' "$check_runs_json" "$status_json" | jq -rs \
    --arg current_job "$CURRENT_JOB_NAME" \
    --arg patterns "$IGNORED_NAME_PATTERNS" '
    .[0] as $all_runs
    | .[1] as $all_statuses
    | ($patterns | split("\n") | map(select(length > 0))) as $pats
    | def is_ignored($name):
        ($name == $current_job) or any($pats[]; . as $p | ($name | test("^(?:" + $p + ")$")));
    ($all_runs | map(select((.name // "") as $n | is_ignored($n) | not))) as $runs
    | ($all_statuses | map(select((.context // "") as $c | is_ignored($c) | not))) as $statuses
    | ($runs | any(.status != "completed")) as $runs_pending
    | ($runs | any(.status == "completed" and ((.conclusion // "") as $c | ["failure", "timed_out", "action_required", "cancelled", "startup_failure", "stale"] | index($c) != null))) as $runs_failed
    | ($statuses | any(.state == "pending")) as $statuses_pending
    | ($statuses | any(.state == "failure" or .state == "error")) as $statuses_failed
    | if $runs_failed or $statuses_failed then
        "done"
      elif (($runs | length) == 0 and ($statuses | length) == 0) then
        "pending"
      elif ($runs_pending or $statuses_pending) then
        "pending"
      else
        "done"
      end
  '; then
    echo "Warning: failed to evaluate check status; treating checks as pending." >&2
    echo "pending"
  fi
}

main() {
  local avg_longest wait_seconds elapsed remaining_retries status

  avg_longest="$(compute_avg_longest_seconds)"
  if [[ "$avg_longest" -le 0 ]]; then
    echo "Could not determine average longest job duration, using minimum delay ${MIN_WAIT_SECONDS}s."
    wait_seconds="$MIN_WAIT_SECONDS"
  else
    wait_seconds=$((avg_longest / 2))
    if [[ "$wait_seconds" -gt "$MAX_INITIAL_WAIT_SECONDS" ]]; then
      wait_seconds="$MAX_INITIAL_WAIT_SECONDS"
    fi
    if [[ "$wait_seconds" -lt "$MIN_WAIT_SECONDS" ]]; then
      wait_seconds="$MIN_WAIT_SECONDS"
    fi
    echo "Average longest run duration: ${avg_longest}s. Initial half-time wait: ${wait_seconds}s."
  fi

  elapsed=0
  while [[ "$wait_seconds" -gt "$MIN_WAIT_SECONDS" && "$elapsed" -lt "$MAX_TOTAL_WAIT_SECONDS" ]]; do
    echo "Waiting ${wait_seconds}s before checking CI status (elapsed: ${elapsed}s)."
    "$SLEEP_CMD" "$wait_seconds"
    elapsed=$((elapsed + wait_seconds))

    status="$(check_ci_status)"
    if [[ "$status" == "done" ]]; then
      remaining_retries="$(compute_remaining_retries "$elapsed")"
      echo "All checks completed or a failure occurred. Handing off immediately with ${remaining_retries} max retries."
      write_output 0 "$remaining_retries" "$MIN_WAIT_SECONDS"
      return 0
    fi

    wait_seconds=$((wait_seconds / 2))
    if [[ "$wait_seconds" -lt "$MIN_WAIT_SECONDS" ]]; then
      wait_seconds="$MIN_WAIT_SECONDS"
    fi
  done

  if [[ "$elapsed" -ge "$MAX_TOTAL_WAIT_SECONDS" ]]; then
    write_output 0 1 "$MIN_WAIT_SECONDS"
    return 0
  fi

  remaining_retries="$(compute_remaining_retries "$elapsed")"
  echo "Reached ${MIN_WAIT_SECONDS}s threshold. Handing off with ${wait_seconds}s next delay and ${remaining_retries} retries."
  write_output "$wait_seconds" "$remaining_retries" "$MIN_WAIT_SECONDS"
}

main "$@"

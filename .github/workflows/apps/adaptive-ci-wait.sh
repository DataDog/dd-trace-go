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

  if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
    {
      echo "next-delay-seconds=${next_delay}"
      echo "remaining-retries=${remaining_retries}"
    } >> "$GITHUB_OUTPUT"
  else
    echo "next-delay-seconds=${next_delay}"
    echo "remaining-retries=${remaining_retries}"
  fi
}

# Fetch recent successful pull_request workflow runs and return the average
# duration in seconds of the top N longest runs, excluding this workflow.
compute_avg_longest_seconds() {
  local runs_json
  if ! runs_json="$(gh api "repos/${GITHUB_REPOSITORY}/actions/runs?event=pull_request&status=success&per_page=100" 2>/dev/null)"; then
    echo "0"
    return 0
  fi

  jq -r --argjson top_n "$TOP_RUNS_COUNT" '
    (.workflow_runs // [])
    | map(select(.path != ".github/workflows/all-green.yml"))
    | map(select(.run_started_at != null and .updated_at != null))
    | map((.updated_at | fromdateiso8601) - (.run_started_at | fromdateiso8601))
    | map(select(. > 0))
    | sort_by(-.)
    | .[:$top_n]
    | if length == 0 then 0 else (add / length | floor) end
  ' <<< "$runs_json" 2>/dev/null || echo "0"
}

# Return "done" when all non-ignored checks have finished or any has failed.
# Return "pending" when checks are still running or when status cannot be read.
check_ci_status() {
  local check_runs_json status_json

  if ! check_runs_json="$(gh api --paginate "repos/${GITHUB_REPOSITORY}/commits/${TARGET_SHA}/check-runs?per_page=100" 2>/dev/null | jq -s '[.[].check_runs[]?]' 2>/dev/null)"; then
    echo "pending"
    return 0
  fi

  if ! status_json="$(gh api "repos/${GITHUB_REPOSITORY}/commits/${TARGET_SHA}/status?per_page=100" 2>/dev/null)"; then
    echo "pending"
    return 0
  fi

  jq -r -n \
    --argjson check_runs "$check_runs_json" \
    --argjson commit_status "$status_json" \
    --arg current_job "$CURRENT_JOB_NAME" \
    --arg patterns "$IGNORED_NAME_PATTERNS" '
    ($patterns | split("\n") | map(select(length > 0))) as $pats
    | def is_ignored($name):
        ($name == $current_job) or any($pats[]; . as $p | ($name | test("^(?:" + $p + ")$")));
    ($check_runs | map(select((.name // "") as $n | is_ignored($n) | not))) as $runs
    | (($commit_status.statuses // []) | map(select((.context // "") as $c | is_ignored($c) | not))) as $statuses
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
  ' 2>/dev/null || echo "pending"
}

main() {
  local avg_longest wait_seconds elapsed remaining_seconds remaining_retries status

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
      echo "All checks completed or a failure occurred. Handing off immediately."
      write_output 0 1
      return 0
    fi

    wait_seconds=$((wait_seconds / 2))
    if [[ "$wait_seconds" -lt "$MIN_WAIT_SECONDS" ]]; then
      wait_seconds="$MIN_WAIT_SECONDS"
    fi
  done

  remaining_seconds=$((MAX_TOTAL_WAIT_SECONDS - elapsed))
  if [[ "$remaining_seconds" -le 0 ]]; then
    write_output 0 1
    return 0
  fi

  remaining_retries=$(((remaining_seconds + MIN_WAIT_SECONDS - 1) / MIN_WAIT_SECONDS))
  if [[ "$remaining_retries" -lt 1 ]]; then
    remaining_retries=1
  fi

  echo "Reached ${MIN_WAIT_SECONDS}s threshold. Handing off with ${wait_seconds}s next delay and ${remaining_retries} retries."
  write_output "$wait_seconds" "$remaining_retries"
}

main "$@"

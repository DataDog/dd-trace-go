#!/bin/bash
# Turns macOS/Windows failures of the Orchestrion and OTelc integration
# lanes into tracked GitHub issues. Called by ci-lane-triage.yml after
# those legs left pull requests (they run on main-based events only).
#
# One open issue per failed leg, deduplicated by lane and leg name; a leg
# that goes green closes its issue; one tracking issue summarizes every
# lane's last status; the scheduled run alerts when a lane went 26 hours
# without any run on main (a missing nightly).
#
# Environment (set by ci-lane-triage.yml):
#   GH_TOKEN   token with issues:write
#   REPO       owner/name
#   RUN_ID     workflow_run id of the lane run (empty on schedule/dispatch)
#   HEAD_SHA   head sha of that run (empty on schedule/dispatch)

set -euo pipefail

readonly FAILURE_LABEL='ci/lane-failure'
readonly DASHBOARD_LABEL='ci/lane-dashboard'
readonly TRACKING_TITLE='macOS/Windows integration lanes — main-based runs'
readonly TEAM='@DataDog/apm-go'
# The lanes this script triages, as named by the workflows filter in
# ci-lane-triage.yml.
readonly -a LANES=('Orchestrion' 'OTelc')
# A lane is missing when no run happened within this many hours.
readonly MISSING_HOURS='26'
# macOS/Windows legs match these words in the job name.
readonly -a LEG_WORDS=('macos' 'windows')

log() { printf '%s %s\n' "$(date -u +%H:%M:%S)" "$*"; }

is_lane_leg() {
  local name=$1 word
  for word in "${LEG_WORDS[@]}"; do
    [[ $name == *"$word"* ]] && return 0
  done
  return 1
}

ensure_label() {
  gh label create "$1" --color "$2" --repo "$REPO" >/dev/null 2>&1 || true
}

# Prints the issue number of an open, labeled issue with the exact title,
# or nothing when there is no match. gh's --jq takes a fixed expression,
# so the title is passed through jq itself.
find_issue() {
  local title=$1 label=$2
  gh issue list --state open --label "$label" --repo "$REPO" \
    --json number,title --limit 200 2>/dev/null \
    | jq -r --arg t "$title" '.[] | select(.title == $t) | .number' || true
}

lane_run_url() {
  printf 'https://github.com/%s/actions/runs/%s' "$REPO" "$1"
}

dd_ci_link() {
  # The same query the dd-ci-upload bot posts on pull requests.
  printf 'https://app.datadoghq.com/ci/pipeline-executions'
  printf '?query=ci_level%%3Ajob+%%40git.commit.sha%%3A%s' "$1"
}

commit_line() {
  local sha=$1 subject
  subject=$(gh api "repos/$REPO/commits/$sha" \
    --jq '.commit.message' 2>/dev/null | head -n 1) || subject=''
  printf '%s — %s' "${sha:0:10}" "${subject:-<no subject>}"
}

# Legs of the lane run with the given conclusion, one job name per line.
# gh api --jq is a fixed expression, so the conclusion goes through jq.
legs_with_conclusion() {
  local conclusion=$1
  gh api "repos/$REPO/actions/runs/$RUN_ID/jobs?per_page=100" 2>/dev/null \
    | jq -r --arg c "$conclusion" \
      '.jobs[] | select(.conclusion == $c) | .name' || true
}

issue_body() {
  local lane=$1 name=$2 attribution=''
  # The previous main-based run decides whether this failure is the first
  # red: a green previous run means the failing commit introduced it.
  local prev
  prev=$(gh run list --workflow "$lane" --branch main --limit 20 \
    --json conclusion,headSha,event \
    --jq '[.[] | select(.event != "pull_request")] | .[1] // empty' \
    2>/dev/null) || prev=''
  if [ -n "$prev" ]; then
    local prev_conclusion prev_sha
    prev_conclusion=$(printf '%s' "$prev" | jq -r '.conclusion')
    prev_sha=$(printf '%s' "$prev" | jq -r '.headSha')
    if [ "$prev_conclusion" = 'success' ]; then
      attribution="Last green main run was $(commit_line "$prev_sha"),"
      attribution+=' so this commit is the first red.'
    else
      attribution="Previous main run $(commit_line "$prev_sha") was"
      attribution+=' also not green, so the breakage may be older.'
    fi
  fi
  cat <<EOF
A macOS or Windows leg of the **$lane** lane failed on main. These legs run
on main-based events only, so this failure did not gate a pull request.

Failing run: $(lane_run_url "$RUN_ID")
Commit: $(commit_line "$HEAD_SHA")
Datadog CI: $(dd_ci_link "$HEAD_SHA")
${attribution}

$TEAM please triage.
EOF
}

triage_run() {
  local lane
  lane=$(gh api "repos/$REPO/actions/runs/$RUN_ID" \
    --jq '.name' 2>/dev/null) || lane=''
  [ -n "$lane" ] || {
    log "run $RUN_ID not readable; skipping"
    return 0
  }

  local name issue body
  while IFS= read -r name; do
    [ -n "$name" ] || continue
    is_lane_leg "$name" || continue
    issue=$(find_issue "lane: $lane — $name" "$FAILURE_LABEL")
    if [ -n "$issue" ]; then
      log "still failing: $lane / $name; commenting on issue #$issue"
      body="Failed again at $(commit_line "$HEAD_SHA"):"
      body+=" $(lane_run_url "$RUN_ID")"
      gh issue comment "$issue" --repo "$REPO" --body "$body" >/dev/null
    else
      log "opening issue: lane: $lane — $name"
      gh issue create --repo "$REPO" --title "lane: $lane — $name" \
        --label "$FAILURE_LABEL" \
        --body "$(issue_body "$lane" "$name")" >/dev/null
    fi
  done < <(legs_with_conclusion 'failure')

  while IFS= read -r name; do
    [ -n "$name" ] || continue
    is_lane_leg "$name" || continue
    issue=$(find_issue "lane: $lane — $name" "$FAILURE_LABEL")
    if [ -n "$issue" ]; then
      log "green: $lane / $name; closing issue #$issue"
      body="Green again at $(commit_line "$HEAD_SHA"):"
      body+=" $(lane_run_url "$RUN_ID"). Closing."
      gh issue comment "$issue" --repo "$REPO" --body "$body" >/dev/null
      gh issue close "$issue" --repo "$REPO" >/dev/null
    fi
  done < <(legs_with_conclusion 'success')
}

# One status line per lane for the tracking issue body.
last_run_line() {
  local lane=$1 run id conclusion n_mw n_green
  run=$(gh run list --workflow "$lane" --branch main --limit 20 \
    --json databaseId,conclusion,event \
    --jq '[.[] | select(.event != "pull_request")] | .[0] // empty' \
    2>/dev/null) || run=''
  if [ -z "$run" ]; then
    printf 'no run on main recorded yet\n'
    return 0
  fi
  id=$(printf '%s' "$run" | jq -r '.databaseId')
  conclusion=$(printf '%s' "$run" | jq -r '.conclusion // "unknown"')
  n_mw=$(gh api "repos/$REPO/actions/runs/$id/jobs?per_page=100" --jq \
    '[.jobs[] | select(.name | test("macos|windows"; "i"))] | length' \
    2>/dev/null || printf 0)
  n_green=$(gh api "repos/$REPO/actions/runs/$id/jobs?per_page=100" --jq \
    '[.jobs[] | select(.conclusion == "success")
      | select(.name | test("macos|windows"; "i"))] | length' \
    2>/dev/null || printf 0)
  printf 'last run %s — %s, %s of %s macOS/Windows legs green\n' \
    "$(lane_run_url "$id")" "$conclusion" "$n_green" "$n_mw"
}

tracking_body() {
  local lane number title any=0
  {
    printf 'Status of the macOS/Windows legs of the Orchestrion and OTelc'
    printf ' integration lanes (main-based runs only).\n\n'
    for lane in "${LANES[@]}"; do
      printf '## %s\n' "$lane"
      last_run_line "$lane"
      printf '\n'
    done
    printf '## Open failures\n\n'
    while IFS=$'\t' read -r number title; do
      [ -n "$number" ] || continue
      printf -- '- %s %s\n' "$number" "$title"
      any=1
    done < <(gh issue list --state open --label "$FAILURE_LABEL" \
      --repo "$REPO" --json number,title \
      --jq '.[] | [.number, .title] | @tsv' 2>/dev/null || true)
    [ "$any" -eq 1 ] || printf 'None.\n'
  }
}

refresh_tracking_issue() {
  local issue footer
  footer='Kept up to date by the CI Lane Triage workflow; do not close.'
  issue=$(find_issue "$TRACKING_TITLE" "$DASHBOARD_LABEL")
  if [ -n "$issue" ]; then
    log "refreshing tracking issue #$issue"
    gh issue edit "$issue" --repo "$REPO" \
      --body "$(tracking_body)" >/dev/null
  else
    log 'creating tracking issue'
    gh issue create --repo "$REPO" --title "$TRACKING_TITLE" \
      --label "$DASHBOARD_LABEL" \
      --body "$(printf '%s\n\n%s\n' "$(tracking_body)" "$footer")" \
      >/dev/null
  fi
}

missing_run_alert() {
  local lane since found issue body
  since=$(date -u -d "-${MISSING_HOURS} hours" +%Y-%m-%dT%H:%M:%SZ)
  since=${since:-$(date -u -v-26H +%Y-%m-%dT%H:%M:%SZ)}
  for lane in "${LANES[@]}"; do
    found=$(gh run list --workflow "$lane" --branch main \
      --created ">=$since" --json databaseId,event \
      --jq '[.[] | select(.event != "pull_request")] | length' \
      2>/dev/null) || found=0
    if [ "${found:-0}" -eq 0 ]; then
      log "missing-run alert for $lane"
      issue=$(find_issue "$TRACKING_TITLE" "$DASHBOARD_LABEL")
      if [ -n "$issue" ]; then
        body="Alert: no **$lane** run on main in the last"
        body+=" ${MISSING_HOURS} hours — the nightly may be missing. $TEAM"
        gh issue comment "$issue" --repo "$REPO" --body "$body" >/dev/null
      fi
    fi
  done
}

main() {
  command -v gh >/dev/null || {
    log 'gh not installed'
    exit 1
  }
  # Before any create: gh issue create fails outright on a missing label,
  # and the first real triage must not die on one.
  ensure_label "$FAILURE_LABEL" 'D93F0B'
  ensure_label "$DASHBOARD_LABEL" '0E8A16'
  if [ -n "${RUN_ID:-}" ]; then
    triage_run
  else
    missing_run_alert
  fi
  refresh_tracking_issue
}

main "$@"

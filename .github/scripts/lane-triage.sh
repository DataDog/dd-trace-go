#!/bin/bash
# Turns macOS/Windows failures of the Orchestrion and OTelc integration
# lanes into tracked GitHub issues. Called by ci-lane-triage.yml after
# those legs left pull requests (they run on main-based events only).
#
# One open issue per failed leg (failure and timeout conclusions),
# deduplicated by lane and leg name to the lowest-numbered open issue;
# a leg that goes green closes its issue only when its run is the lane's
# newest completed run; issues for legs that left the matrix are closed;
# one tracking issue, reopened when closed, summarizes every lane's last
# status; the scheduled run alerts when a lane went 26 hours without
# any run on main. Every gh read goes through read_gh: three attempts,
# then the job fails loudly instead of silently dropping a triage.
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
# macOS/Windows legs match these words in the job name; the triage loops
# and the status counts build their name regex from this one list.
readonly -a LEG_WORDS=('macos' 'windows')

log() { printf '%s %s\n' "$(date -u +%H:%M:%S)" "$*"; }

# read_gh: every GitHub read retried three times, then fail the job. A
# swallowed read silently drops a failure issue, opens a duplicate team
# ping, or alerts a missing nightly that ran.
read_gh() {
  local out i
  for i in 1 2 3; do
    out=$(gh "$@") && {
      printf '%s' "$out"
      return 0
    }
    log "gh $1 failed, attempt $i of 3" >&2
  done
  exit 1
}

# The name regex shared by every leg test, built from LEG_WORDS.
leg_name_regex() {
  local IFS='|'
  printf '%s' "${LEG_WORDS[*]}"
}

is_lane_leg() {
  local name=$1
  [[ $name =~ $(leg_name_regex) ]]
}

ensure_label() {
  gh label create "$1" --color "$2" --repo "$REPO" >/dev/null 2>&1 || true
}

# All issues, open or closed by state, with the exact title and label,
# lowest number first.
issues_with_title() {
  local state=$1 title=$2 label=$3
  read_gh issue list --state "$state" --label "$label" --repo "$REPO" \
    --json number,title --limit 200 \
    | jq -r --arg t "$title" \
      '[.[] | select(.title == $t) | .number] | sort | .[]'
}

# The lowest-numbered open issue with the exact title and label, or
# nothing when there is no match. Two racing triage runs can leave two
# open duplicates briefly; the lowest number keeps every later command
# single-valued while collapse_duplicates heals them.
find_issue() {
  issues_with_title open "$1" "$2" | head -n 1
}

# Two triage runs can race each other past the lookup: both see no
# issue and both create one. Healing is cheaper than locking: keep the
# lowest-numbered issue, close the rest as duplicates.
collapse_duplicates() {
  # The here-string expands before the loop body runs, so the list is
  # initialized here, never inside the body.
  local title=$1 label=$2 first=1 issue list
  list=$(issues_with_title open "$title" "$label")
  while IFS= read -r issue; do
    [ -n "$issue" ] || continue
    if [ "$first" -eq 1 ]; then
      first=0
      continue
    fi
    log "closing duplicate issue #$issue for '$title'"
    gh issue close "$issue" --repo "$REPO" --comment \
      'Closed as a duplicate created by a race between two triage
runs; the lowest-numbered issue stays open.' \
      >/dev/null
  done <<<"$list"
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
  subject=$(read_gh api "repos/$REPO/commits/$sha" \
    --jq '.commit.message' | head -n 1)
  printf '%s — %s' "${sha:0:10}" "$subject"
}

# Legs of the lane run with any of the given conclusions, one job name
# per line. Timed-out legs must join the failures: GitHub records a job
# that exceeded its timeout as timed_out, not failure, and a hung
# platform leg would otherwise open no issue at all.
legs_with_conclusion() {
  local conclusions
  conclusions=$(printf '%s' "$*" | jq -R 'split(" ")')
  read_gh api "repos/$REPO/actions/runs/$RUN_ID/jobs?per_page=100" \
    | jq -r --argjson c "$conclusions" \
      '.jobs[] | select(.conclusion == $c[]) | .name'
}

issue_body() {
  local lane=$1 name=$2 attribution=''
  # The predecessor run decides whether this failure is the first red: a
  # green predecessor means the failing commit introduced it. Anchor to
  # the triggering run, and skip cancelled and empty conclusions, which
  # would otherwise hide the last green commit behind "also not green".
  local prev
  prev=$(read_gh run list --workflow "$lane" --branch main --limit 20 \
    --json databaseId,conclusion,headSha,event \
    | jq -c --argjson id "$RUN_ID" '
      [.[] | select(.event != "pull_request")
           | select(.event != "workflow_dispatch")
           | select(.conclusion != null and .conclusion != ""
               and .conclusion != "cancelled")]
      | (map(.databaseId) | index($id)) as $i
      | if $i == null then empty else .[$i + 1] // empty end') || prev=''
  if [ -n "$prev" ]; then
    local prev_conclusion prev_sha
    prev_conclusion=$(printf '%s' "$prev" | jq -r '.conclusion')
    prev_sha=$(printf '%s' "$prev" | jq -r '.headSha')
    if [ "$prev_conclusion" = 'success' ]; then
      attribution="Last green main run was $(
        set -e
        commit_line "$prev_sha"
      ),"
      attribution+=' so this commit is the first red.'
    else
      attribution="Previous main run $(
        set -e
        commit_line "$prev_sha"
      ) was"
      attribution+=' also not green, so the breakage may be older.'
    fi
  fi
  cat <<EOF
A macOS or Windows leg of the **$lane** lane failed on main. These legs
run on main-based events only, so this failure did not gate a pull
request.

Failing run: $(lane_run_url "$RUN_ID")
Commit: $(
    set -e
    commit_line "$HEAD_SHA"
  )
Datadog CI: $(dd_ci_link "$HEAD_SHA")
${attribution}

$TEAM please triage.
EOF
}

# The lane's newest completed run on main (any conclusion), or nothing.
# Scheduled runs race pushes: a run that is still queued or running
# tells nothing about the current state of main.
newest_completed_lane_run() {
  local lane=$1
  # Dispatch runs with a pinned version are deliberate experiments, not
  # main's state, so they count for no chronology here (the same
  # reasoning that keeps them out of the triage trigger).
  read_gh run list --workflow "$lane" --branch main --limit 20 \
    --json databaseId,conclusion,event \
    | jq -c '[.[] | select(.event != "pull_request")
                 | select(.event != "workflow_dispatch")
                 | select(.conclusion != null and .conclusion != ""
                     and .conclusion != "cancelled")]
      | .[0] // empty'
}

# The job conclusions of a run, keyed by job name. A missing jobs page
# reads as an empty map.
run_job_conclusions() {
  local id=$1
  read_gh api "repos/$REPO/actions/runs/$id/jobs?per_page=100" \
    | jq -c '[.jobs[] | {key: .name, value: .conclusion}]
      | from_entries'
}

# A green leg may only close its issue when this run is the lane's
# newest completed on main: an older run whose triage finishes after a
# newer failure must not close the newer issue.
is_newest_completed_lane_run() {
  local lane=$1 latest
  latest=$(newest_completed_lane_run "$lane" | jq -r '.databaseId // empty')
  [ -n "$latest" ] && [ "$latest" = "$RUN_ID" ]
}

# True when the issue's record carries a newer recovery: a "Green
# again" comment with a run id above this run's. An older failure's
# reopen must not undo a newer run's recovery close.
issue_has_newer_recovery() {
  local issue=$1 record newer
  record=$(read_gh issue view "$issue" --repo "$REPO" \
    --json body,comments \
    --jq '.body, (.comments[] | .body // "")') || exit 1
  local greens
  greens=$(printf '%s' "$record" \
    | grep -E 'Green again' || true)
  for newer in $(printf '%s' "$greens" \
    | grep -oE '/actions/runs/[0-9]+' | grep -oE '[0-9]+'); do
    if [ "$newer" -gt "$RUN_ID" ]; then
      return 0
    fi
  done
  return 1
}

# Reopen a failure issue only when no newer run has recovered the leg
# and the live state is closed. gh fails a reopen of an open issue, so
# the state read keeps the common no-op case off the error path, and a
# real reopen failure stops the job instead of reporting success with
# the failure left hidden.
reopen_unless_recovered() {
  local issue=$1 state
  issue_has_newer_recovery "$issue" && return 0
  state=$(read_gh issue view "$issue" --repo "$REPO" \
    --json state --jq '.state')
  [ "$state" = 'CLOSED' ] || return 0
  log "issue closed: reopening #$issue"
  gh issue reopen "$issue" --repo "$REPO" >/dev/null
  # Reconcile after the mutation: a newer recovery can close the issue
  # between the check above and the reopen. A failure recording after
  # this re-read is covered by its own triage's reopen, so every
  # interleaving converges to the newest state.
  if issue_has_newer_recovery "$issue"; then
    log "newer recovery recorded during reopen: closing #$issue"
    local note
    note='Reclosed: a newer recovery recorded while this reopen'
    note+=" was in flight."
    gh issue close "$issue" --repo "$REPO" --comment "$note" \
      >/dev/null
  fi
}

# True when the issue's body or comments mention a run id newer than
# this run's: a newer failure spoke on record, and no close may bury it.
issue_has_newer_failure() {
  local issue=$1 record newer
  # A read that exhausts its retries must abort recovery: an empty
  # record would read as "no newer failure" and close an issue whose
  # history could not be verified.
  record=$(read_gh issue view "$issue" --repo "$REPO" \
    --json body,comments \
    --jq '.body, (.comments[] | .body // "")') || exit 1
  # Only failure records count: recovery comments ("Green again")
  # from newer runs must not read as failures, or an older triage
  # would reopen a healthy issue a newer recovery already closed.
  local failures
  failures=$(printf '%s' "$record" \
    | grep -E 'Failing run:|Failed again' || true)
  for newer in $(printf '%s' "$failures" \
    | grep -oE '/actions/runs/[0-9]+' | grep -oE '[0-9]+'); do
    if [ "$newer" -gt "$RUN_ID" ]; then
      return 0
    fi
  done
  return 1
}

# The legs of the newest completed run that are green, one name per
# line: a failure whose leg a newer run already fixed needs no ping.
newest_green_legs() {
  local lane=$1 newest id
  newest=$(newest_completed_lane_run "$lane")
  id=$(printf '%s' "$newest" | jq -r '.databaseId // empty')
  [ -n "$id" ] || return 0
  run_job_conclusions "$id" \
    | jq -r --arg re "$(leg_name_regex)" \
      'to_entries[] | select(.value == "success")
        | select(.key | test($re; "i")) | .key'
}

triage_run() {
  local lane
  lane=$(read_gh api "repos/$REPO/actions/runs/$RUN_ID" --jq '.name')
  [ -n "$lane" ] || {
    log "run $RUN_ID has no lane name; skipping"
    return 0
  }

  local name issue body failed
  failed=$(legs_with_conclusion failure timed_out)
  if [ -n "$failed" ]; then
    # A newer completed run may have already fixed the leg: opening an
    # issue would ping the team for something recovered.
    local recovered
    recovered=$(
      set -e
      newest_green_legs "$lane"
    )
    while IFS= read -r name; do
      [ -n "$name" ] || continue
      if grep -qxF "$name" <<<"$recovered"; then
        log "newer run already green: $lane / $name; not opening"
        continue
      fi
      is_lane_leg "$name" || continue
      issue=$(find_issue "lane: $lane — $name" "$FAILURE_LABEL")
      if [ -z "$issue" ]; then
        # A recovery may have closed this issue after the failure
        # opened it. Reopen rather than mint a duplicate, but only
        # when no newer run recovered the leg: a close by a newer
        # recovery is real, and this reopen must not undo it.
        issue=$(issues_with_title all \
          "lane: $lane — $name" "$FAILURE_LABEL" | head -n 1)
        if [ -n "$issue" ]; then
          reopen_unless_recovered "$issue"
        fi
      fi
      if [ -n "$issue" ]; then
        log "still failing: $lane / $name; commenting on issue #$issue"
        body="Failed again at $(
          set -e
          commit_line "$HEAD_SHA"
        ):"
        body+=" $(lane_run_url "$RUN_ID")"
        gh issue comment "$issue" --repo "$REPO" --body "$body" >/dev/null
        # A recovery can close the issue between this triage's lookup
        # and its comment; the guarded reopen restores visibility.
        reopen_unless_recovered "$issue"
      else
        log "opening issue: lane: $lane — $name"
        body=$(
          set -e
          issue_body "$lane" "$name"
        )
        gh issue create --repo "$REPO" --title "lane: $lane — $name" \
          --label "$FAILURE_LABEL" \
          --body "$body" >/dev/null
      fi
      # A concurrent triage run may have created the same issue in the
      # meantime: keep the lowest number.
      collapse_duplicates "lane: $lane — $name" "$FAILURE_LABEL"
    done <<<"$failed"
  fi

  # Only the lane's newest completed run may announce a recovery: an
  # older run whose triage finishes after a newer failure must not
  # close the newer issue.
  if ! is_newest_completed_lane_run "$lane"; then
    log "older run: not closing issues for green legs of $lane"
    return 0
  fi

  local green
  green=$(legs_with_conclusion success)
  if [ -n "$green" ]; then
    while IFS= read -r name; do
      [ -n "$name" ] || continue
      is_lane_leg "$name" || continue
      issue=$(find_issue "lane: $lane — $name" "$FAILURE_LABEL")
      if [ -n "$issue" ]; then
        # Reconcile against the issue's own record, not another
        # freshness check: this triage may have paused anywhere around
        # the close, and a newer failure that triaged in the meantime
        # left its run id in the issue. Any run id above this run's
        # means a newer failure is on record, and closing would bury
        # it; that failure's path reopens what a stale close shuts.
        if issue_has_newer_failure "$issue"; then
          log "newer failure on record: not closing issue #$issue"
          continue
        fi
        log "green: $lane / $name; closing issue #$issue"
        body="Green again at $(
          set -e
          commit_line "$HEAD_SHA"
        ):"
        body+=" $(lane_run_url "$RUN_ID"). Closing."
        gh issue comment "$issue" --repo "$REPO" --body "$body" >/dev/null
        gh issue close "$issue" --repo "$REPO" >/dev/null
        # Reconcile after the mutation: a newer failure may have
        # recorded while this close was in flight. A failure recording
        # after this re-read is covered by its own triage's reopen,
        # so every interleaving converges to the newer state.
        if issue_has_newer_failure "$issue"; then
          log "newer failure recorded during close: reopening #$issue"
          gh issue reopen "$issue" --repo "$REPO" >/dev/null
          body="Reopened: a newer failure recorded while this"
          body+=" close was in flight."
          gh issue comment "$issue" --repo "$REPO" --body "$body" \
            >/dev/null
        fi
      fi
    done <<<"$green"
  fi

  # Legs leave the matrix (a Go release bump renames or drops strata),
  # and their issues would sit under "Open failures" forever because the
  # green loop only closes legs the current run still has. Only a
  # successful run proves the matrix enumerated: a failed
  # go-versions-matrix produces no platform jobs at all, and retiring
  # every issue then would erase the failures the run should report.
  local run_conclusion
  run_conclusion=$(read_gh api "repos/$REPO/actions/runs/$RUN_ID" \
    --jq '.conclusion // ""')
  if [ "$run_conclusion" = 'success' ]; then
    close_absent_legs "$lane"
  else
    log "run not successful: not retiring absent legs of $lane"
  fi
}

# Close every open failure issue of this lane whose leg name no longer
# appears among the current run's job names.
close_absent_legs() {
  local lane=$1 issue title leg jobnames open
  # Both inputs initialize before their loops: the here-strings expand
  # before the first iteration runs.
  jobnames=$(run_job_conclusions "$RUN_ID" | jq -r 'keys[]')
  open=$(read_gh issue list --state open \
    --label "$FAILURE_LABEL" --repo "$REPO" \
    --json number,title --limit 200 \
    | jq -r --arg l "$lane" \
      '.[] | select(.title | startswith("lane: " + $l + " — "))
        | "\(.number)\t\(.title)"')
  while IFS=$'\t' read -r issue title; do
    [ -n "$issue" ] || continue
    leg=${title#* — }
    if grep -qxF "$leg" <<<"$jobnames"; then
      continue
    fi
    log "leg left the matrix: closing issue #$issue"
    gh issue close "$issue" --repo "$REPO" --comment \
      "The ${lane} lane no longer runs a leg with this name; closing. \
Reopen or open a new issue if the leg returns." \
      >/dev/null
  done <<<"$open"
}

# One status line per lane for the tracking issue body.
last_run_line() {
  local lane=$1 run id conclusion n_mw n_green
  run=$(newest_completed_lane_run "$lane")
  if [ -z "$run" ]; then
    printf 'no completed run on main recorded yet\n'
    return 0
  fi
  id=$(printf '%s' "$run" | jq -r '.databaseId')
  conclusion=$(printf '%s' "$run" | jq -r '.conclusion // "unknown"')
  # One jobs fetch feeds both counts, and the name regex comes from the
  # one LEG_WORDS list, so the counts and the triaged legs cannot drift
  # apart after a rename.
  local jobs re
  jobs=$(run_job_conclusions "$id")
  re=$(leg_name_regex)
  n_mw=$(printf '%s' "$jobs" | jq --arg re "$re" \
    '[keys[] | select(test($re; "i"))] | length')
  n_green=$(printf '%s' "$jobs" | jq --arg re "$re" \
    '[to_entries[] | select(.value == "success")
      | select(.key | test($re; "i"))] | length')
  printf 'last run %s — %s, %s of %s macOS/Windows legs green\n' \
    "$(lane_run_url "$id")" "$conclusion" "$n_green" "$n_mw"
}

tracking_body() {
  local lane any=0 number title open
  # Initialize before the block: a failed read here aborts the
  # rendering instead of printing empty content over the issue.
  open=$(read_gh issue list --state open \
    --label "$FAILURE_LABEL" --repo "$REPO" \
    --json number,title --limit 200 \
    | jq -r '.[] | [.number, .title] | @tsv')
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
      printf -- '- #%s %s\n' "$number" "$title"
      any=1
    done <<<"$open"
    [ "$any" -eq 1 ] || printf 'None.\n'
  }
}

refresh_tracking_issue() {
  local issue footer tb body
  footer='Kept up to date by the CI Lane Triage workflow; do not close.'
  issue=$(find_issue "$TRACKING_TITLE" "$DASHBOARD_LABEL")
  tb=$(
    set -e
    tracking_body
  )
  body="$(printf '%s\n\n%s\n' "$tb" "$footer")"
  if [ -n "$issue" ]; then
    log "refreshing tracking issue #$issue"
    gh issue edit "$issue" --repo "$REPO" --body "$body" >/dev/null
    return 0
  fi
  # A closed tracking issue must be reopened, not duplicated: the
  # healing only sees open issues.
  issue=$(issues_with_title all "$TRACKING_TITLE" "$DASHBOARD_LABEL" \
    | head -n 1)
  if [ -n "$issue" ]; then
    log "reopening tracking issue #$issue"
    gh issue reopen "$issue" --repo "$REPO" >/dev/null
    gh issue edit "$issue" --repo "$REPO" --body "$body" >/dev/null
    return 0
  fi
  log 'creating tracking issue'
  gh issue create --repo "$REPO" --title "$TRACKING_TITLE" \
    --label "$DASHBOARD_LABEL" --body "$body" >/dev/null
  collapse_duplicates "$TRACKING_TITLE" "$DASHBOARD_LABEL"
}

missing_run_alert() {
  local lane since found issue body
  since=$(date -u -d "-${MISSING_HOURS} hours" +%Y-%m-%dT%H:%M:%SZ)
  for lane in "${LANES[@]}"; do
    found=$(read_gh run list --workflow "$lane" --branch main \
      --created ">=$since" --json databaseId,event \
      | jq '[.[] | select(.event != "pull_request"
        and .event != "workflow_dispatch")] | length')
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
  # Before any create: gh issue create fails outright on a missing
  # label, and the first real triage must not die on one.
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

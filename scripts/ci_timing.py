# Unless explicitly stated otherwise all files in this repository are licensed
# under the Apache License Version 2.0.
# This product includes software developed at Datadog (https://www.datadoghq.com/).
# Copyright 2026 Datadog, Inc.
#!/usr/bin/env python3
"""Collect and compare GitHub Actions CI timing and cache evidence.

Authored for the dd-trace-go Go build-cache measurement program. GitHub
completed-run data is the source of truth; Datadog series are secondary.

Subcommands:

  collect --repo OWNER/NAME --since DATE --until DATE --output-dir DIR
      Walks completed workflow runs in the window using authenticated `gh api`
      read operations, and writes:
        - observations.jsonl: one deduplicated record per (run, attempt, job),
          plus one pr_feedback record per (PR, head SHA) revision.
        - cache_snapshot.json: cache usage and entry inventory for the day.
        - summary.csv: per-workload counts and durations.
      Collect is idempotent: rerunning it over an overlapping window merges
      new records and keeps existing ones (dedup key: run, attempt, job id).

  compare --baseline DIR --candidate DIR --output-dir DIR
      Compares two collected windows using baseline-frozen strata and
      baseline-frequency weights, and writes comparison.md and comparison.csv.

Metric definitions (these names are part of the report contract):

  job wall time     completed_at - started_at from the GitHub jobs API,
                    covering main steps and post steps of the job.
  post time         log time from the first "Post job cleanup." marker to the
                    last log line; covers post steps including cache saving.
  restore result    classification from the structured `cache-observation:`
                    record emitted by .github/actions/setup-go: exact,
                    prefix, cold_miss, disabled, error, or unknown.
  save result       classification from job log markers, per save event:
                    saved, exact_key_skip, conflict, error; the job-level
                    aggregate keeps errors over conflicts over successes. A
                    successful job does not prove a successful save.
  pr feedback time  earliest workflow-run created_at for a PR revision to the
                    completion of the last non-ignored check run on that
                    revision, including queueing and the all-green delay.
                    Human review and merge-queue waiting are excluded by
                    ignoring the same check-name patterns as the all-green
                    workflow does.

Raw job logs are input data only. They are never executed, sourced, or
republished in full; only extracted fields above enter reports.

Only the Python standard library is used. Requires `gh` (authenticated) for
collection; compare runs fully offline.
"""

import argparse
import csv
import datetime as dt
import hashlib
import json
import math
import re
import statistics
import subprocess
import sys
from pathlib import Path

SCHEMA = 1

# Check-name patterns the all-green workflow ignores; mirror them here so PR
# feedback excludes human-review and merge-queue waiting exactly like CI does.
IGNORED_CHECK_PATTERNS = (
    "devflow/merge",
    "devflow/mergegate",
    "label_issues",
    "check-title",
    "dd-gitlab/",
    "DDCI Status",
)

LOG_TS = re.compile(
    r"^(?:\ufeff)?(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z)(?:\s+(.*))?$"
)
CACHE_OBSERVATION = re.compile(r"cache-observation:(\{.*\})$")
SAVE_EXACT_SKIP = re.compile(
    r"Cache hit occurred on the primary key .*not saving cache"
)
SAVE_CONFLICT = re.compile(
    r"Failed to save: Unable to reserve cache with key .*another job may be creating"
)
SAVE_ERROR = re.compile(r"Failed to save")
# Verified against real runner logs: actions/cache v6 writes
# "Cache saved with key: ..." and actions/setup-go's post step writes
# "Cache saved with the key: ...".
SAVE_SAVED = re.compile(r"Cache saved with (?:the )?key:")
POST_MARKER = "Post job cleanup."

OS_HINTS = ("ubuntu", "windows", "macos", "linux")
ARCH_HINTS = ("x64", "arm64", "x86", "amd64", "arm")


def iso(value):
    """Parse a GitHub timestamp, accepting log ns-precision fractions.

    API timestamps look like 2026-09-13T11:36:55Z; log line timestamps
    carry nanoseconds (2026-09-13T11:37:05.8370630Z) which strptime's
    %f cannot take whole, so the fraction is truncated to microseconds.
    """
    if not value:
        return None
    match = re.match(r"^(.{19})(?:\.(\d+))?Z$", str(value).strip())
    if not match:
        return None
    base, frac = match.group(1), match.group(2)
    try:
        if frac:
            parsed = dt.datetime.strptime(
                base + "." + frac[:6] + "+0000", "%Y-%m-%dT%H:%M:%S.%f%z"
            )
        else:
            parsed = dt.datetime.strptime(base + "+0000", "%Y-%m-%dT%H:%M:%S%z")
    except ValueError:
        return None
    return parsed.replace(tzinfo=None)


def safe_int(value, default):
    try:
        return int(value)
    except (TypeError, ValueError):
        return default


def seconds(start, end):
    if start is None or end is None:
        return None
    return (end - start).total_seconds()


class GhError(Exception):
    pass


class GhClient:
    """Thin wrapper over `gh api` with manual pagination."""

    def __init__(self, repo, verbose=False):
        self.repo = repo
        self.verbose = verbose

    def _run(self, args):
        cmd = ["gh", "api"] + args
        proc = subprocess.run(cmd, capture_output=True, text=True)
        if proc.returncode != 0:
            raise GhError(proc.stderr.strip() or proc.stdout.strip())
        return proc.stdout

    def get_json(self, path):
        out = self._run(["-H", "Accept: application/vnd.github+json", path])
        try:
            return json.loads(out)
        except ValueError as exc:
            raise GhError(f"non-JSON response from {path}: {exc}") from exc

    def get_text(self, path):
        # Job logs contain terminal escape sequences and a UTF-8 BOM; gh only
        # emits them with --allow-escape-sequences, which older gh lacks.
        try:
            return self._run(["--allow-escape-sequences", path])
        except GhError:
            return self._run([path])

    def paginate(self, path):
        """Yield items from a list endpoint, following ?page=N pagination."""
        base = path
        sep = "&" if "?" in base else "?"
        page = 1
        while True:
            data = self.get_json(f"{base}{sep}per_page=100&page={page}")
            items = (
                data.get("items")
                or data.get("jobs")
                or data.get("workflow_runs")
                or data.get("check_runs")
                or data.get("actions_caches")
                or []
            )
            yield from items
            if len(items) < 100:
                return
            page += 1


# --------------------------------------------------------------------------
# Log parsing
# --------------------------------------------------------------------------


def parse_log(log_text):
    """Extract cache evidence from one job's log. Returns a dict or None."""
    if log_text is None:
        return None
    lines = log_text.splitlines()
    ts = []
    post_start = None
    last = None
    observation = None
    for line in lines:
        m = LOG_TS.match(line)
        if not m:
            continue
        when = iso(m.group(1))
        if when is None:
            continue
        ts.append(when)
        if post_start is None and POST_MARKER in (m.group(2) or ""):
            post_start = when
        last = when
        if observation is None:
            om = CACHE_OBSERVATION.search(m.group(2) or "")
            if om:
                try:
                    observation = json.loads(om.group(1))
                except ValueError:
                    observation = None
    post_seconds = seconds(post_start, last) if post_start else None
    body = "\n".join(lines)
    return {
        "observation": observation,
        "post_seconds": post_seconds,
        "saves": classify_saves(body),
        "save_result": aggregate_save(classify_saves(body)),
        "has_logs": True,
    }


def classify_saves(body):
    """Classify every cache save event in the log, in order.

    One job typically saves several caches (repo, tools, toolchain, build
    caches), so a single job-level label would misreport mixed outcomes.
    """
    saves = []
    for line in body.splitlines():
        if SAVE_SAVED.search(line):
            saves.append("saved")
        elif SAVE_CONFLICT.search(line):
            saves.append("conflict")
        elif SAVE_EXACT_SKIP.search(line):
            saves.append("exact_key_skip")
        elif SAVE_ERROR.search(line):
            saves.append("error")
    return saves


def aggregate_save(saves):
    """Reduce per-event save results to one job-level label.

    Errors dominate conflicts, conflicts dominate successes: a job that
    saved one cache but lost another to a reservation conflict is more
    usefully reported as a conflict. An empty list stays `unknown` — a
    successful job does not prove a successful save.
    """
    for result in ("error", "conflict"):
        if result in saves:
            return result
    if "saved" in saves:
        return "saved"
    if "exact_key_skip" in saves:
        return "exact_key_skip"
    return "unknown"


def classify_restore(obs):
    """Classify one restore observation from provider outputs.

    Ambiguous evidence is `unknown`, never a guessed hit or miss. For the
    GitHub provider `cache_hit` is an exact-hit boolean and a non-empty
    `cache_matched_key` alongside `cache_hit != true` marks a prefix restore.
    """
    if obs is None:
        return "unknown"
    if str(obs.get("enabled", "")).lower() != "true":
        return "disabled"
    outcome = obs.get("outcome", "")
    if outcome == "":
        return "unknown"
    if outcome != "success":
        return "error"
    hit = str(obs.get("cache_hit", "")).lower()
    if "cache_matched_key" in obs:
        if hit == "true":
            return "exact"
        if hit == "false" or hit == "":
            return "prefix" if obs.get("cache_matched_key") else "cold_miss"
        return "unknown"
    if hit == "true":
        return "exact"
    if hit == "false":
        return "cold_miss"
    return "unknown"


def restore_records(observation):
    """Turn the structured observation into classified restore records."""
    if observation is None:
        return [], None, None
    restores = []
    for entry in observation.get("restores", []):
        restores.append(
            {"name": entry.get("name", ""), "result": classify_restore(entry)}
        )
    runtime = observation.get("runtime", {}) or {}
    workload = {
        "provider": observation.get("provider", "unknown"),
        "family": observation.get("workload", ""),
        "requested_go_version": runtime.get("requested", ""),
        "resolved_go_version": runtime.get("version", ""),
    }
    return restores, workload, None


def os_from_labels(labels):
    for label in labels or []:
        for hint in OS_HINTS:
            if hint in label.lower():
                return hint
    return "unknown"


def arch_from_labels(labels):
    for label in labels or []:
        for hint in ARCH_HINTS:
            if hint in label.lower():
                return "x64" if hint == "amd64" else hint
    return "unknown"


def logical_job_name(name):
    """Strip the workflow display prefix from a job name."""
    return name.split(" / ")[-1] if name else name


# --------------------------------------------------------------------------
# Collection
# --------------------------------------------------------------------------


def collect_run(client, run, collected_at):
    """Produce job observations for one workflow run, all attempts."""
    records = []
    run_id = run["id"]
    latest_attempt = safe_int(run.get("run_attempt"), 1)
    for attempt in range(1, latest_attempt + 1):
        path = f"repos/{client.repo}/actions/runs/{run_id}/attempts/{attempt}/jobs"
        try:
            jobs = list(client.paginate(path))
        except GhError as exc:
            print(
                f"  ! run {run_id} attempt {attempt}: jobs unavailable: {exc}",
                file=sys.stderr,
            )
            continue
        for job in jobs:
            records.append(
                build_job_record(
                    client, run, attempt, latest_attempt, job, collected_at
                )
            )
    return records


def build_job_record(client, run, attempt, latest_attempt, job, collected_at):
    run_id = run["id"]
    latest_attempt = safe_int(latest_attempt, attempt)
    job_id = job["id"]
    try:
        log_text = client.get_text(f"repos/{client.repo}/actions/jobs/{job_id}/logs")
    except GhError:
        log_text = None
    log = parse_log(log_text)
    labels = job.get("labels") or []
    restores, workload, _ = restore_records(log["observation"] if log else None)
    started = iso(job.get("started_at"))
    completed = iso(job.get("completed_at"))
    prs = run.get("pull_requests") or []
    record = {
        "kind": "job_observation",
        "schema": SCHEMA,
        "collected_at": collected_at,
        "run": {
            "id": run_id,
            "attempt": attempt,
            "latest_attempt": attempt == latest_attempt,
            "event": run.get("event", ""),
            "status": run.get("status", ""),
            "conclusion": run.get("conclusion", ""),
            "workflow": run.get("name", ""),
            "workflow_file": workflow_file(run.get("path", "")),
            "created_at": run.get("created_at", ""),
            "head_sha": run.get("head_sha", ""),
            "head_branch": run.get("head_branch", ""),
            "pr": prs[0]["number"] if prs else None,
            "url": run.get("html_url", ""),
        },
        "job": {
            "id": job_id,
            "name": job.get("name", ""),
            "logical_name": logical_job_name(job.get("name", "")),
            "status": job.get("status", ""),
            "conclusion": job.get("conclusion", ""),
            "started_at": job.get("started_at", ""),
            "completed_at": job.get("completed_at", ""),
            "seconds": seconds(started, completed),
            "runner_group": job.get("runner_group_name", ""),
            "runner_os": os_from_labels(labels),
            "runner_arch": arch_from_labels(labels),
            "runner_labels": labels,
        },
        "workload": workload or {"provider": "unknown", "family": ""},
        "cache": {
            "restores": restores,
            "saves": log["saves"] if log else [],
            "save_result": log["save_result"] if log else "unknown",
            "post_seconds": log["post_seconds"] if log else None,
        },
        "steps": [
            {
                "name": s.get("name", ""),
                "number": s.get("number"),
                "conclusion": s.get("conclusion", ""),
                "seconds": seconds(
                    iso(s.get("started_at")), iso(s.get("completed_at"))
                ),
            }
            for s in job.get("steps") or []
        ],
        "has_logs": bool(log),
    }
    return record


def workflow_file(path):
    return path.rsplit("/", 1)[-1] if path else ""


def is_ignored_check(name):
    return any(pattern in name for pattern in IGNORED_CHECK_PATTERNS)


def collect_pr_feedback(client, runs, collected_at):
    """One feedback record per (PR, head SHA): earliest workflow creation to
    last non-ignored check completion."""
    by_revision = {}
    for run in runs:
        prs = run.get("pull_requests") or []
        if not prs or run.get("event") != "pull_request":
            continue
        key = (prs[0]["number"], run.get("head_sha", ""))
        by_revision.setdefault(key, []).append(run)
    records = []
    for (pr, sha), group in sorted(
        by_revision.items(), key=lambda kv: (kv[0][0], kv[0][1])
    ):
        try:
            checks = list(
                client.paginate(f"repos/{client.repo}/commits/{sha}/check-runs")
            )
        except GhError as exc:
            print(
                f"  ! pr {pr} {sha[:8]}: check-runs unavailable: {exc}", file=sys.stderr
            )
            continue
        relevant = [c for c in checks if not is_ignored_check(c.get("name", ""))]
        completions = []
        for check in relevant:
            when = iso(check.get("completed_at"))
            if when is not None:
                completions.append(when)
        created_values = []
        for run in group:
            when = iso(run.get("created_at"))
            if when is not None:
                created_values.append(when)
        created = min(created_values) if created_values else None
        last = max(completions) if completions else None
        signature = hashlib.sha256(
            "\n".join(
                sorted(c.get("name", "") for c in relevant if c.get("name"))
            ).encode()
        ).hexdigest()[:16]
        records.append(
            {
                "kind": "pr_feedback",
                "schema": SCHEMA,
                "collected_at": collected_at,
                "pr": pr,
                "head_sha": sha,
                "created_at": created.isoformat() + "Z" if created else "",
                "last_check_completed_at": last.isoformat() + "Z" if last else "",
                "seconds": seconds(created, last),
                "selection_signature": signature,
                "checks_count": len(relevant),
                "run_ids": sorted({r["id"] for r in group}),
            }
        )
    return records


def collect_cache_snapshot(client, collected_at):
    usage = client.get_json(f"repos/{client.repo}/actions/cache/usage")
    entries = list(client.paginate(f"repos/{client.repo}/actions/caches"))
    return {
        "kind": "cache_snapshot",
        "schema": SCHEMA,
        "collected_at": collected_at,
        "usage": {
            "active_caches_size_in_bytes": usage.get("active_caches_size_in_bytes"),
            "active_caches_count": usage.get("active_caches_count"),
        },
        "entries": [
            {
                "id": e.get("id"),
                "key": e.get("key", ""),
                "ref": e.get("ref", ""),
                "size_in_bytes": e.get("size_in_bytes"),
                "created_at": e.get("created_at", ""),
                "last_accessed_at": e.get("last_accessed_at", ""),
            }
            for e in entries
        ],
    }


def load_existing_observations(output_dir):
    path = Path(output_dir) / "observations.jsonl"
    existing = {}
    existing_pr = set()
    if path.exists():
        for line in path.read_text().splitlines():
            if not line.strip():
                continue
            try:
                rec = json.loads(line)
            except ValueError:
                continue
            if rec.get("kind") == "job_observation":
                key = (rec["run"]["id"], rec["run"]["attempt"], rec["job"]["id"])
                existing[key] = rec
            elif rec.get("kind") == "pr_feedback":
                existing_pr.add((rec["pr"], rec["head_sha"]))
    return existing, existing_pr, path


def write_observations(new_job_records, new_feedback_records, existing, path):
    merged = dict(existing)
    for rec in new_job_records:
        merged[(rec["run"]["id"], rec["run"]["attempt"], rec["job"]["id"])] = rec
    job_lines = [json.dumps(rec, sort_keys=True) for rec in merged.values()]
    fb_lines = [json.dumps(rec, sort_keys=True) for rec in new_feedback_records]
    path.write_text("\n".join(job_lines + fb_lines) + "\n")


def cmd_collect(args):
    client = GhClient(args.repo, verbose=args.verbose)
    collected_at = dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    out = Path(args.output_dir)
    out.mkdir(parents=True, exist_ok=True)

    runs = []
    for page_run in client.paginate(
        f"repos/{args.repo}/actions/runs?created={args.since}..{args.until}"
        "&status=completed&exclude_pull_requests=false"
    ):
        if args.workflows:
            f = workflow_file(page_run.get("path", ""))
            if f not in args.workflows:
                continue
        if args.events and page_run.get("event") not in args.events:
            continue
        runs.append(page_run)
    print(
        f"Collected {len(runs)} completed runs for {args.repo} "
        f"({args.since}..{args.until})"
    )

    records = []
    for run in runs:
        if args.verbose:
            print(
                f"  run {run['id']} {run.get('name', '')} attempt {run.get('run_attempt')}"
            )
        records.extend(collect_run(client, run, collected_at))
    records.extend(collect_pr_feedback(client, runs, collected_at))
    job_records = [r for r in records if r["kind"] == "job_observation"]
    print(
        f"  {len(job_records)} job observations, "
        f"{len(records) - len(job_records)} pr feedback records"
    )

    existing, existing_pr, obs_path = load_existing_observations(out)
    new_jobs = []
    new_feedback = []
    for rec in records:
        if rec["kind"] == "job_observation":
            key = (rec["run"]["id"], rec["run"]["attempt"], rec["job"]["id"])
            if key in existing:
                continue
            new_jobs.append(rec)
        else:
            key = (rec["pr"], rec["head_sha"])
            if key in existing_pr:
                continue
            new_feedback.append(rec)
    write_observations(new_jobs, new_feedback, existing, obs_path)
    print(f"  wrote {len(new_jobs) + len(new_feedback)} new records to {obs_path}")

    if args.cache_snapshot:
        snapshot = collect_cache_snapshot(client, collected_at)
        snap_path = out / "cache_snapshot.json"
        snap_path.write_text(json.dumps(snapshot, sort_keys=True, indent=2) + "\n")
        print(f"  cache snapshot: {snapshot['usage']} -> {snap_path}")

    all_jobs = list(existing.values()) + new_jobs
    write_summary(out, all_jobs)
    return 0


def write_summary(out, job_records):
    """Per-workload summary CSV over successful first-attempt jobs."""
    path = out / "summary.csv"
    groups = {}
    for rec in job_records:
        if rec["run"]["attempt"] != 1 or not rec["run"]["latest_attempt"]:
            continue
        if rec["job"]["conclusion"] != "success":
            continue
        key = (
            rec["run"]["workflow_file"],
            rec["workload"].get("family") or rec["job"]["logical_name"],
            rec["job"]["runner_os"],
            rec["job"]["runner_arch"],
            rec["workload"].get("resolved_go_version", ""),
        )
        groups.setdefault(key, []).append(rec["job"]["seconds"])
    with path.open("w", newline="") as fh:
        writer = csv.writer(fh)
        writer.writerow(
            [
                "workflow_file",
                "workload",
                "os",
                "arch",
                "go_version",
                "successful_first_attempts",
                "median_seconds",
            ]
        )
        for key in sorted(groups):
            values = [v for v in groups[key] if v is not None]
            if not values:
                continue
            writer.writerow(
                list(key) + [len(values), round(statistics.median(values), 1)]
            )
    print(f"  summary -> {path}")


# --------------------------------------------------------------------------
# Comparison
# --------------------------------------------------------------------------


def median(values):
    return statistics.median(values) if values else None


def percentile(sorted_values, p):
    if not sorted_values:
        return None
    rank = (len(sorted_values) - 1) * p
    low = math.floor(rank)
    high = math.ceil(rank)
    if low == high:
        return sorted_values[low]
    return sorted_values[low] + (sorted_values[high] - sorted_values[low]) * (
        rank - low
    )


def stratify(rec):
    return (
        rec["run"]["workflow_file"],
        rec["workload"].get("family") or rec["job"]["logical_name"],
        rec["job"]["runner_os"],
        rec["job"]["runner_arch"],
        rec["workload"].get("resolved_go_version", ""),
    )


def perf_values(records):
    """Successful first attempts only; failures are reported separately."""
    values = {}
    failures = {}
    for rec in records:
        if rec["kind"] != "job_observation":
            continue
        key = stratify(rec)
        if rec["run"]["attempt"] != 1 or not rec["run"]["latest_attempt"]:
            continue
        if rec["job"]["conclusion"] == "success" and rec["job"]["seconds"] is not None:
            values.setdefault(key, []).append(rec["job"]["seconds"])
        elif rec["job"]["conclusion"] != "skipped":
            failures.setdefault(key, []).append(rec["job"]["conclusion"])
    return values, failures


def weighted_median(pairs):
    """Median of values where each (value, weight) pair carries its own weight."""
    if not pairs:
        return None
    pairs = sorted(pairs)
    total = sum(w for _, w in pairs)
    if total <= 0:
        return None
    cumulative = 0.0
    for value, weight in pairs:
        cumulative += weight
        if cumulative >= total / 2:
            return value
    return pairs[-1][0]


def load_records(directory):
    path = Path(directory) / "observations.jsonl"
    records = []
    for line in path.read_text().splitlines():
        if not line.strip():
            continue
        try:
            records.append(json.loads(line))
        except ValueError as exc:
            raise SystemExit(f"invalid JSONL in {path}: {exc}") from exc
    return records


def cmd_compare(args):
    baseline = load_records(args.baseline)
    candidate = load_records(args.candidate)
    base_vals, base_fail = perf_values(baseline)
    cand_vals, cand_fail = perf_values(candidate)

    # Freeze strata membership and weights from the baseline: each baseline
    # stratum's frequency is its weight; within a stratum observations are
    # equal. This stops job-mix changes from manufacturing improvements.
    weighted_base = []
    weighted_cand = []
    rows = []
    for key in sorted(base_vals):
        b = sorted(base_vals[key])
        c = sorted(cand_vals.get(key, []))
        weight = len(b)
        weighted_base.extend((v, 1.0) for v in b)
        if c:
            per = weight / len(c)
            weighted_cand.extend((v, per) for v in c)
        rows.append(
            {
                "stratum": key,
                "baseline_n": len(b),
                "candidate_n": len(c),
                "baseline_p50": percentile(b, 0.50),
                "baseline_p95": percentile(b, 0.95),
                "candidate_p50": percentile(c, 0.50),
                "candidate_p95": percentile(c, 0.95),
            }
        )
    missing = [key for key in base_vals if key not in cand_vals]

    base_wmed = weighted_median(weighted_base)
    cand_wmed = weighted_median(weighted_cand)
    improvement = None
    if base_wmed and cand_wmed is not None:
        improvement = 1 - cand_wmed / base_wmed

    # PR feedback comparison: match by selection signature, baseline weights.
    base_fb = feedback_by_signature(baseline)
    cand_fb = feedback_by_signature(candidate)
    fb_weighted_base = []
    fb_weighted_cand = []
    fb_rows = []
    for sig in sorted(base_fb):
        b = sorted(v for v in base_fb[sig] if v is not None)
        c = sorted(v for v in cand_fb.get(sig, []) if v is not None)
        fb_weighted_base.extend((v, 1.0) for v in b)
        if c:
            fb_weighted_cand.extend((v, len(b) / len(c)) for v in c)
        fb_rows.append(
            {
                "signature": sig,
                "baseline_n": len(b),
                "candidate_n": len(c),
                "baseline_p50": percentile(b, 0.50),
                "candidate_p50": percentile(c, 0.50),
            }
        )
    fb_base_wmed = weighted_median(fb_weighted_base)
    fb_cand_wmed = weighted_median(fb_weighted_cand)
    fb_improvement = None
    if fb_base_wmed and fb_cand_wmed is not None:
        fb_improvement = 1 - fb_cand_wmed / fb_base_wmed

    out = Path(args.output_dir)
    out.mkdir(parents=True, exist_ok=True)
    write_comparison_csv(out, rows, fb_rows)
    report = render_report(
        rows,
        missing,
        base_wmed,
        cand_wmed,
        improvement,
        base_fail,
        cand_fail,
        fb_rows,
        fb_base_wmed,
        fb_cand_wmed,
        fb_improvement,
    )
    (out / "comparison.md").write_text(report)
    print(report)
    print(f"\nWrote {out / 'comparison.md'} and {out / 'comparison.csv'}")
    if improvement is None:
        return 2
    return 0


def feedback_by_signature(records):
    groups = {}
    for rec in records:
        if rec.get("kind") != "pr_feedback":
            continue
        groups.setdefault(rec.get("selection_signature", ""), []).append(
            rec.get("seconds")
        )
    return groups


def write_comparison_csv(out, rows, fb_rows):
    with (out / "comparison.csv").open("w", newline="") as fh:
        writer = csv.writer(fh)
        writer.writerow(
            [
                "workflow_file",
                "workload",
                "os",
                "arch",
                "go_version",
                "baseline_n",
                "candidate_n",
                "baseline_p50_s",
                "baseline_p95_s",
                "candidate_p50_s",
                "candidate_p95_s",
            ]
        )
        for row in rows:
            writer.writerow(
                list(row["stratum"])
                + [
                    row["baseline_n"],
                    row["candidate_n"],
                    row["baseline_p50"],
                    row["baseline_p95"],
                    row["candidate_p50"],
                    row["candidate_p95"],
                ]
            )
        writer.writerow([])
        writer.writerow(
            [
                "pr_signature",
                "baseline_n",
                "candidate_n",
                "baseline_p50_s",
                "candidate_p50_s",
            ]
        )
        for row in fb_rows:
            writer.writerow(
                [
                    row["signature"],
                    row["baseline_n"],
                    row["candidate_n"],
                    row["baseline_p50"],
                    row["candidate_p50"],
                ]
            )


def render_report(
    rows,
    missing,
    base_wmed,
    cand_wmed,
    improvement,
    base_fail,
    cand_fail,
    fb_rows,
    fb_base_wmed,
    fb_cand_wmed,
    fb_improvement,
):
    def fmt(v):
        return "n/a" if v is None else f"{v:.1f}"

    lines = [
        "# CI timing comparison",
        "",
        "Strata and weights are frozen from the baseline window. Only",
        "successful first attempts feed the performance table; failures and",
        "retries are reported separately below.",
        "",
        "## Job duration",
        "",
        "| workflow | workload | os | go | base n | cand n | base p50 | "
        "cand p50 | base p95 | cand p95 |",
        "|---|---|---|---|---|---|---|---|---|---|",
    ]
    for row in rows:
        s = row["stratum"]
        lines.append(
            f"| {s[0]} | {s[1]} | {s[2]} | {s[4] or 'n/a'} | "
            f"{row['baseline_n']} | {row['candidate_n']} | "
            f"{fmt(row['baseline_p50'])} | {fmt(row['candidate_p50'])} | "
            f"{fmt(row['baseline_p95'])} | {fmt(row['candidate_p95'])} |"
        )
    lines += [
        "",
        f"- baseline weighted median: {fmt(base_wmed)} s",
        f"- candidate weighted median: {fmt(cand_wmed)} s",
        f"- **improvement: {fmt(improvement * 100) if improvement is not None else 'n/a'}%**",
    ]
    if missing:
        lines += [
            "",
            "## Missing coverage",
            "",
            "Baseline strata with no candidate observations:",
        ]
        lines += [f"- {' / '.join(k)}" for k in missing]
    fail_counts = {k: len(v) for k, v in base_fail.items()}
    cand_fail_counts = {k: len(v) for k, v in cand_fail.items()}
    if fail_counts or cand_fail_counts:
        lines += ["", "## Non-success outcomes (first attempts)", ""]
        for key in sorted(set(fail_counts) | set(cand_fail_counts)):
            lines.append(
                f"- {' / '.join(key)}: baseline {fail_counts.get(key, 0)}, "
                f"candidate {cand_fail_counts.get(key, 0)}"
            )
    lines += [
        "",
        "## PR feedback time",
        "",
        "| signature | base n | cand n | base p50 s | cand p50 s |",
        "|---|---|---|---|---|",
    ]
    for row in fb_rows:
        lines.append(
            f"| {row['signature']} | {row['baseline_n']} | {row['candidate_n']} | "
            f"{fmt(row['baseline_p50'])} | {fmt(row['candidate_p50'])} |"
        )
    lines += [
        "",
        f"- baseline weighted median: {fmt(fb_base_wmed)} s",
        f"- candidate weighted median: {fmt(fb_cand_wmed)} s",
        f"- **improvement: {fmt(fb_improvement * 100) if fb_improvement is not None else 'n/a'}%**",
        "",
        "Weekly observations demonstrate operational improvement, not",
        "randomized causal proof. A workload comparison needs at least five",
        "independent successful first-attempt run IDs per stratum per window;",
        "PR feedback needs five comparable PR revisions per window.",
    ]
    return "\n".join(lines) + "\n"


# --------------------------------------------------------------------------
# CLI
# --------------------------------------------------------------------------


def main(argv=None):
    doc = (__doc__ or "CI timing collector").splitlines()[0]
    parser = argparse.ArgumentParser(description=doc)
    sub = parser.add_subparsers(dest="command", required=True)

    p_collect = sub.add_parser(
        "collect", help="collect completed runs into a directory"
    )
    p_collect.add_argument("--repo", required=True, help="OWNER/NAME")
    p_collect.add_argument("--since", required=True, help="YYYY-MM-DD (inclusive)")
    p_collect.add_argument("--until", required=True, help="YYYY-MM-DD (inclusive)")
    p_collect.add_argument("--output-dir", required=True)
    p_collect.add_argument(
        "--workflows", default="", help="comma-separated workflow file names to keep"
    )
    p_collect.add_argument(
        "--events",
        default="",
        help="comma-separated event names to keep (e.g. pull_request)",
    )
    p_collect.add_argument(
        "--cache-snapshot",
        action="store_true",
        help="also snapshot cache usage and entries",
    )
    p_collect.add_argument("--verbose", action="store_true")
    p_collect.set_defaults(func=cmd_collect)

    p_compare = sub.add_parser("compare", help="compare two collected windows")
    p_compare.add_argument("--baseline", required=True, help="baseline output dir")
    p_compare.add_argument("--candidate", required=True, help="candidate output dir")
    p_compare.add_argument("--output-dir", required=True)
    p_compare.set_defaults(func=cmd_compare)

    args = parser.parse_args(argv)
    if getattr(args, "workflows", ""):
        args.workflows = {w.strip() for w in args.workflows.split(",") if w.strip()}
    if getattr(args, "events", ""):
        args.events = {e.strip() for e in args.events.split(",") if e.strip()}
    return args.func(args)


if __name__ == "__main__":
    sys.exit(main())

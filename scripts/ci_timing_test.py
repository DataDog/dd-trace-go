# Unless explicitly stated otherwise all files in this repository are licensed
# under the Apache License Version 2.0.
# This product includes software developed at Datadog (https://www.datadoghq.com/).
# Copyright 2026 Datadog, Inc.
"""Fixture-based tests for scripts/ci_timing.py.

Run with: python3 -m unittest discover -s scripts -p 'ci_timing_*.py'
"""

import json
import tempfile
import unittest
from pathlib import Path

import ci_timing

FIXTURES = Path(__file__).parent / "ci_timing_fixtures"


def make_run(run_id, attempt, event, workflow, created_at, head_sha, **extra):
    run = {
        "id": run_id,
        "run_attempt": attempt,
        "event": event,
        "name": workflow,
        "path": f".github/workflows/{workflow}",
        "status": "completed",
        "conclusion": "success",
        "created_at": created_at,
        "head_sha": head_sha,
        "head_branch": "feature",
        "html_url": f"https://example/runs/{run_id}",
        "pull_requests": [{"number": 42}],
    }
    run.update(extra)
    return run


def make_job(
    job_id, name, started, completed, conclusion="success", labels=None, steps=None
):
    return {
        "id": job_id,
        "name": name,
        "status": "completed",
        "conclusion": conclusion,
        "started_at": started,
        "completed_at": completed,
        "labels": labels or ["ubuntu-latest"],
        "runner_group_name": "GitHub Actions",
        "runner_name": "runner-1",
        "steps": steps or [],
    }


def observation_line(payload):
    return json.dumps(payload, sort_keys=True)


class FakeClient:
    """Stands in for GhClient with canned endpoint responses."""

    def __init__(self, repo, runs, jobs_by_run, logs_by_job, check_runs=None):
        self.repo = repo
        self.runs = runs
        self.jobs_by_run = jobs_by_run
        self.logs_by_job = logs_by_job
        self.check_runs = check_runs or {}

    def get_json(self, path) -> dict:
        if "actions/cache/usage" in path:
            return {
                "active_caches_size_in_bytes": 1024,
                "active_caches_count": 2,
            }
        if "/actions/caches" in path:
            return {"total_count": 2, "actions_caches": []}
        raise AssertionError(f"unexpected json path: {path}")

    def get_text(self, path):
        for job_id, text in self.logs_by_job.items():
            if str(job_id) in path:
                return text
        raise ci_timing.GhError("logs unavailable")

    def paginate(self, path):
        if "/actions/caches" in path:
            yield from self.get_json(path).get("actions_caches", [])
            return
        if "/actions/runs?" in path:
            yield from self.runs
            return
        if "/commits/" in path and "/check-runs" in path:
            for sha, checks in self.check_runs.items():
                if sha in path:
                    yield from checks
            return
        for key, jobs in self.jobs_by_run.items():
            run_id, attempt = key
            if f"/runs/{run_id}/attempts/{attempt}/jobs" in path:
                yield from jobs
                return
        raise AssertionError(f"unexpected paginate path: {path}")


def log_line(ts, message):
    return f"{ts} {message}"


def base_log_lines(observation=None):
    lines = [
        log_line("2026-09-13T11:37:05.8370630Z", "runner version"),
        log_line("2026-09-13T11:37:07.1327354Z", "##[group]Run actions/cache"),
        log_line("2026-09-13T11:37:09.0478561Z", "Cache restored successfully"),
    ]
    if observation:
        lines.append(log_line("2026-09-13T11:37:10.0000000Z", observation))
    return lines


def post_log_lines(marker="Cache hit occurred on the primary key k, not saving cache."):
    return [
        log_line("2026-09-13T11:37:29.7644082Z", "Post job cleanup."),
        log_line("2026-09-13T11:37:30.2097642Z", marker),
        log_line("2026-09-13T11:37:30.2236184Z", "Cleaning up orphan processes"),
    ]


def setup_observation(cache_hit="", matched=None, enabled="true", outcome="success"):
    restore = {
        "name": "setup_go",
        "enabled": enabled,
        "outcome": outcome,
        "cache_hit": cache_hit,
    }
    if matched is not None:
        restore["cache_matched_key"] = matched
    return {
        "provider": "github-cache",
        "workload": "unit-core",
        "runtime": {"name": "go", "requested": "1.27", "version": "1.27.1"},
        "restores": [restore],
    }


def restore_entry(**kwargs):
    """A single restore entry, as classify_restore expects."""
    return setup_observation(**kwargs)["restores"][0]


class TestClassifyRestore(unittest.TestCase):
    def test_exact_hit(self):
        self.assertEqual(
            ci_timing.classify_restore(restore_entry(cache_hit="true")), "exact"
        )

    def test_prefix_restore(self):
        self.assertEqual(
            ci_timing.classify_restore(restore_entry(cache_hit="false", matched="k-1")),
            "prefix",
        )

    def test_cold_miss(self):
        self.assertEqual(
            ci_timing.classify_restore(restore_entry(cache_hit="false", matched="")),
            "cold_miss",
        )

    def test_disabled(self):
        self.assertEqual(
            ci_timing.classify_restore(restore_entry(enabled="false")), "disabled"
        )

    def test_error(self):
        self.assertEqual(
            ci_timing.classify_restore(restore_entry(outcome="failure")), "error"
        )

    def test_unknown_without_observation(self):
        self.assertEqual(ci_timing.classify_restore(None), "unknown")

    def test_unknown_ambiguous_cache_hit(self):
        self.assertEqual(
            ci_timing.classify_restore(restore_entry(cache_hit="weird")), "unknown"
        )

    def test_missing_outputs_without_matched_key_are_cold_miss(self):
        # The GitHub provider's cache-hit is a plain miss boolean.
        self.assertEqual(
            ci_timing.classify_restore(restore_entry(cache_hit="false")), "cold_miss"
        )


class TestClassifySave(unittest.TestCase):
    def test_exact_key_skip(self):
        log = "\n".join(post_log_lines())
        self.assertEqual(ci_timing.classify_saves(log), ["exact_key_skip"])
        self.assertEqual(ci_timing.aggregate_save(["exact_key_skip"]), "exact_key_skip")

    def test_saved_with_key_marker(self):
        # Real actions/cache v6 marker, verified against runner logs.
        log = "\n".join(post_log_lines("Cache saved with key: datadog-ci-cli-win-x64"))
        self.assertEqual(ci_timing.classify_saves(log), ["saved"])

    def test_saved_with_the_key_marker(self):
        # actions/setup-go's post step writes a different marker.
        log = "\n".join(
            post_log_lines("Cache saved with the key: setup-go-macOS-arm64-go-1.27.1")
        )
        self.assertEqual(ci_timing.classify_saves(log), ["saved"])

    def test_mixed_save_events_are_all_recorded(self):
        # One Windows-style job: two saves plus an exact-key skip.
        log = "\n".join(
            post_log_lines("Cache saved with key: datadog-ci-cli-win-x64")
            + post_log_lines(
                "Cache hit occurred on the primary key k, not saving cache."
            )
            + post_log_lines("Cache saved with key: gitdb-1")
        )
        self.assertEqual(
            ci_timing.classify_saves(log), ["saved", "exact_key_skip", "saved"]
        )

    def test_conflict(self):
        log = "\n".join(
            post_log_lines(
                "Failed to save: Unable to reserve cache with key k, "
                "another job may be creating this cache."
            )
        )
        self.assertEqual(ci_timing.classify_saves(log), ["conflict"])
        self.assertEqual(ci_timing.aggregate_save(["conflict"]), "conflict")

    def test_error(self):
        log = "\n".join(post_log_lines("Failed to save: something else"))
        self.assertEqual(ci_timing.classify_saves(log), ["error"])

    def test_error_dominates_other_outcomes(self):
        self.assertEqual(
            ci_timing.aggregate_save(["saved", "error", "exact_key_skip"]), "error"
        )

    def test_unknown_when_no_evidence(self):
        self.assertEqual(ci_timing.classify_saves("nothing here"), [])
        self.assertEqual(ci_timing.aggregate_save([]), "unknown")

    def test_successful_job_does_not_imply_saved(self):
        # A green job with no save markers must stay unknown, never "saved".
        log = "\n".join(base_log_lines())
        self.assertEqual(ci_timing.classify_saves(log), [])
        self.assertEqual(ci_timing.aggregate_save([]), "unknown")


class TestParseLog(unittest.TestCase):
    def test_post_seconds_and_observation(self):
        obs = setup_observation(cache_hit="true")
        log_text = "\n".join(
            base_log_lines("cache-observation:" + observation_line(obs))
            + post_log_lines()
        )
        parsed = ci_timing.parse_log(log_text)
        self.assertIsNotNone(parsed)
        assert parsed is not None
        self.assertAlmostEqual(
            parsed["post_seconds"], 30.2236184 - 29.7644082, places=3
        )
        self.assertEqual(parsed["observation"]["workload"], "unit-core")
        self.assertEqual(parsed["save_result"], "exact_key_skip")

    def test_bom_first_line(self):
        log_text = "\ufeff" + "\n".join(base_log_lines() + post_log_lines())
        parsed = ci_timing.parse_log(log_text)
        self.assertIsNotNone(parsed)
        assert parsed is not None
        self.assertIsNotNone(parsed["post_seconds"])

    def test_missing_logs_yield_unknown_save(self):
        self.assertIsNone(ci_timing.parse_log(None))

    def test_secret_lines_are_not_propagated(self):
        secret_log = "\n".join(
            base_log_lines()
            + [log_line("2026-09-13T11:37:20Z", "DD_API_KEY=deadbeef gho_abc123")]
            + post_log_lines()
        )
        parsed = ci_timing.parse_log(secret_log)
        self.assertNotIn("deadbeef", json.dumps(parsed))
        self.assertNotIn("gho_abc123", json.dumps(parsed))


class TestCollectRun(unittest.TestCase):
    def _records(self, run, jobs, logs):
        client = FakeClient("DataDog/dd-trace-go", [run], {(run["id"], 1): jobs}, logs)
        return ci_timing.collect_run(client, run, "2026-09-14T00:00:00Z")

    def test_job_record_shape(self):
        run = make_run(
            1,
            1,
            "pull_request",
            "unit-integration-tests.yml",
            "2026-09-13T11:00:00Z",
            "abc123",
        )
        obs = setup_observation(cache_hit="true")
        log_text = "\n".join(
            base_log_lines("cache-observation:" + observation_line(obs))
            + post_log_lines()
        )
        job = make_job(
            11,
            "PR Unit and Integration Tests (1.27) / test-core",
            "2026-09-13T11:01:00Z",
            "2026-09-13T11:37:30Z",
        )
        records = self._records(run, [job], {11: log_text})
        self.assertEqual(len(records), 1)
        rec = records[0]
        self.assertEqual(rec["kind"], "job_observation")
        self.assertEqual(rec["job"]["seconds"], 36 * 60 + 30)
        self.assertEqual(rec["job"]["logical_name"], "test-core")
        self.assertEqual(rec["workload"]["family"], "unit-core")
        self.assertEqual(rec["workload"]["resolved_go_version"], "1.27.1")
        self.assertEqual(rec["cache"]["restores"][0]["result"], "exact")
        self.assertEqual(rec["cache"]["save_result"], "exact_key_skip")
        self.assertAlmostEqual(rec["cache"]["post_seconds"], 0.459, places=3)

    def test_rerun_attempts_recorded(self):
        run = make_run(
            2,
            2,
            "pull_request",
            "unit-integration-tests.yml",
            "2026-09-13T11:00:00Z",
            "abc123",
        )
        failed = make_job(
            21,
            "test-contrib-matrix (chunk 1)",
            "2026-09-13T11:01:00Z",
            "2026-09-13T11:10:00Z",
            conclusion="failure",
        )
        retried = make_job(
            22,
            "test-contrib-matrix (chunk 1)",
            "2026-09-13T11:20:00Z",
            "2026-09-13T11:30:00Z",
        )
        client = FakeClient(
            "DataDog/dd-trace-go",
            [run],
            {(2, 1): [failed], (2, 2): [retried]},
            {21: "\n".join(base_log_lines()), 22: "\n".join(base_log_lines())},
        )
        records = ci_timing.collect_run(client, run, "2026-09-14T00:00:00Z")
        by_key = {(r["run"]["attempt"], r["job"]["id"]): r for r in records}
        self.assertFalse(by_key[(1, 21)]["run"]["latest_attempt"])
        self.assertTrue(by_key[(2, 22)]["run"]["latest_attempt"])
        self.assertEqual(by_key[(1, 21)]["job"]["conclusion"], "failure")

    def test_missing_logs_still_recorded_with_unknown(self):
        run = make_run(
            3, 1, "push", "main-branch-tests.yml", "2026-09-13T11:00:00Z", "d"
        )
        job = make_job(31, "test-core", "2026-09-13T11:01:00Z", "2026-09-13T11:30:00Z")
        client = FakeClient("DataDog/dd-trace-go", [run], {(3, 1): [job]}, {})
        records = ci_timing.collect_run(client, run, "now")
        rec = records[0]
        self.assertFalse(rec["has_logs"])
        self.assertEqual(rec["cache"]["save_result"], "unknown")
        self.assertEqual(rec["cache"]["restores"], [])
        self.assertEqual(rec["workload"]["family"], "")


class TestPagination(unittest.TestCase):
    def test_collect_follows_pages(self):
        runs = [
            make_run(
                10 + i,
                1,
                "pull_request",
                "unit-integration-tests.yml",
                "2026-09-13T11:00:00Z",
                f"sha{i}",
            )
            for i in range(120)
        ]
        client = FakeClient("DataDog/dd-trace-go", runs, {}, {})
        seen = list(client.paginate("repos/x/actions/runs?created=a..b"))
        self.assertEqual(len(seen), 120)


class TestPrFeedback(unittest.TestCase):
    def test_feedback_and_ignored_checks(self):
        runs = [
            make_run(
                1, 1, "pull_request", "all-green.yml", "2026-09-13T11:00:00Z", "sha1"
            ),
            make_run(
                2,
                1,
                "pull_request",
                "unit-integration-tests.yml",
                "2026-09-13T11:00:10Z",
                "sha1",
            ),
        ]
        checks = [
            {
                "name": "all-jobs-are-green",
                "conclusion": "success",
                "completed_at": "2026-09-13T11:29:00Z",
            },
            {
                "name": "devflow/mergegate",
                "conclusion": "success",
                "completed_at": "2026-09-13T18:00:00Z",
            },  # human review wait: ignored
            {
                "name": "test-core",
                "conclusion": "success",
                "completed_at": "2026-09-13T11:20:00Z",
            },
        ]
        client = FakeClient("DataDog/dd-trace-go", runs, {}, {}, {"sha1": checks})
        records = ci_timing.collect_pr_feedback(client, runs, "now")
        self.assertEqual(len(records), 1)
        rec = records[0]
        # earliest workflow creation 11:00:00 -> last non-ignored check 11:29:00
        self.assertEqual(rec["seconds"], 29 * 60)
        self.assertEqual(rec["checks_count"], 2)

    def test_signature_groups_identical_selections(self):
        runs = [
            make_run(
                3,
                1,
                "pull_request",
                "unit-integration-tests.yml",
                "2026-09-13T11:00:00Z",
                "sha9",
            )
        ]
        checks = [
            {
                "name": "test-core",
                "conclusion": "success",
                "completed_at": "2026-09-13T11:10:00Z",
            },
        ]
        client = FakeClient("DataDog/dd-trace-go", runs, {}, {}, {"sha9": checks})
        records = ci_timing.collect_pr_feedback(client, runs, "now")
        groups = ci_timing.feedback_by_signature(
            [
                {
                    "kind": "pr_feedback",
                    "selection_signature": records[0]["selection_signature"],
                    "seconds": 600,
                }
            ]
        )
        self.assertEqual(len(groups), 1)


class TestSizeUnits(unittest.TestCase):
    def test_cache_snapshot_uses_cache_api_bytes(self):
        client = FakeClient("DataDog/dd-trace-go", [], {}, {})
        snap = ci_timing.collect_cache_snapshot(client, "now")
        # size_in_bytes come from the API untouched; no unit conversion here.
        self.assertEqual(snap["usage"]["active_caches_size_in_bytes"], 1024)


class TestCompare(unittest.TestCase):
    def _dir_with(self, records):
        tmp = tempfile.TemporaryDirectory()
        path = Path(tmp.name)
        path.mkdir(parents=True, exist_ok=True)
        with (path / "observations.jsonl").open("w") as fh:
            for rec in records:
                fh.write(json.dumps(rec) + "\n")
        self.addCleanup(tmp.cleanup)
        return str(path)

    def _job_rec(
        self,
        workflow,
        family,
        run_id,
        seconds_value,
        conclusion="success",
        attempt=1,
        latest=True,
    ):
        return {
            "kind": "job_observation",
            "run": {
                "id": run_id,
                "attempt": attempt,
                "latest_attempt": latest,
                "event": "pull_request",
                "conclusion": "success",
                "workflow_file": workflow,
            },
            "job": {
                "id": run_id * 10,
                "logical_name": family,
                "conclusion": conclusion,
                "runner_os": "ubuntu",
                "runner_arch": "x64",
                "seconds": seconds_value,
            },
            "workload": {"family": family, "resolved_go_version": "1.27.1"},
            "cache": {"restores": [], "save_result": "unknown", "post_seconds": None},
        }

    def test_weighted_median_improvement_frozen_strata(self):
        # Stratum A (4 baseline obs of 100s, 4 candidate obs of 50s) and
        # stratum B (2 baseline obs of 500s, 6 candidate obs of 500s).
        # Baseline weights freeze the mix: A weight 4, B weight 2.
        # Improvement must reflect the frozen mixture, not the candidate mix.
        base = [self._job_rec("w.yml", "A", i, 100) for i in range(1, 5)]
        base += [self._job_rec("w.yml", "B", i, 500) for i in range(101, 103)]
        cand = [self._job_rec("w.yml", "A", i, 50) for i in range(201, 205)]
        cand += [self._job_rec("w.yml", "B", i, 500) for i in range(301, 307)]
        baseline_dir = self._dir_with(base)
        candidate_dir = self._dir_with(cand)
        out = tempfile.TemporaryDirectory()
        self.addCleanup(out.cleanup)
        args = type(
            "Args",
            (),
            {
                "baseline": baseline_dir,
                "candidate": candidate_dir,
                "output_dir": out.name,
            },
        )()
        rc = ci_timing.cmd_compare(args)
        self.assertEqual(rc, 0)
        report = (Path(out.name) / "comparison.md").read_text()
        # Frozen-mixture medians: baseline weighted median 100, candidate 50
        # (A carries weight 4, B weight 2; half the weight sits in A).
        self.assertIn("baseline weighted median: 100.0 s", report)
        self.assertIn("candidate weighted median: 50.0 s", report)
        self.assertIn("improvement: 50.0%", report)

    def test_missing_candidate_stratum_reported(self):
        base = [
            self._job_rec("w.yml", "A", 1, 100),
            self._job_rec("w.yml", "C", 2, 100),
        ]
        cand = [self._job_rec("w.yml", "A", 3, 50)]
        baseline_dir = self._dir_with(base)
        candidate_dir = self._dir_with(cand)
        out = tempfile.TemporaryDirectory()
        self.addCleanup(out.cleanup)
        args = type(
            "Args",
            (),
            {
                "baseline": baseline_dir,
                "candidate": candidate_dir,
                "output_dir": out.name,
            },
        )()
        ci_timing.cmd_compare(args)
        report = (Path(out.name) / "comparison.md").read_text()
        self.assertIn("Missing coverage", report)
        self.assertIn("C", report)

    def test_skipped_jobs_are_absent_not_zero(self):
        base = [self._job_rec("w.yml", "A", 1, 100)]
        cand = [self._job_rec("w.yml", "A", 2, 100, conclusion="skipped")]
        baseline_dir = self._dir_with(base)
        candidate_dir = self._dir_with(cand)
        out = tempfile.TemporaryDirectory()
        self.addCleanup(out.cleanup)
        args = type(
            "Args",
            (),
            {
                "baseline": baseline_dir,
                "candidate": candidate_dir,
                "output_dir": out.name,
            },
        )()
        rc = ci_timing.cmd_compare(args)
        report = (Path(out.name) / "comparison.md").read_text()
        # The skipped candidate job contributes no success duration and no
        # zero-duration fake; with no candidate values the comparison is
        # not computable and exits 2.
        self.assertIn("cand n |", report)
        self.assertIn("| 1 | 0 |", report)
        self.assertEqual(rc, 2)

    def test_failures_reported_separately(self):
        base = [
            self._job_rec("w.yml", "A", 1, 100),
            self._job_rec("w.yml", "A", 2, None, conclusion="failure"),
        ]
        cand = [self._job_rec("w.yml", "A", 3, 100)]
        baseline_dir = self._dir_with(base)
        candidate_dir = self._dir_with(cand)
        out = tempfile.TemporaryDirectory()
        self.addCleanup(out.cleanup)
        args = type(
            "Args",
            (),
            {
                "baseline": baseline_dir,
                "candidate": candidate_dir,
                "output_dir": out.name,
            },
        )()
        ci_timing.cmd_compare(args)
        report = (Path(out.name) / "comparison.md").read_text()
        self.assertIn("Non-success outcomes", report)
        self.assertIn("failure", report)

    def test_pr_feedback_improvement(self):
        def fb(sig, seconds_value):
            return {
                "kind": "pr_feedback",
                "pr": 1,
                "head_sha": sig,
                "seconds": seconds_value,
                "selection_signature": sig,
            }

        base = [fb("s1", 3000) for _ in range(5)]
        cand = [fb("s1", 2400) for _ in range(5)]
        baseline_dir = self._dir_with(base)
        candidate_dir = self._dir_with(cand)
        out = tempfile.TemporaryDirectory()
        self.addCleanup(out.cleanup)
        args = type(
            "Args",
            (),
            {
                "baseline": baseline_dir,
                "candidate": candidate_dir,
                "output_dir": out.name,
            },
        )()
        ci_timing.cmd_compare(args)
        report = (Path(out.name) / "comparison.md").read_text()
        self.assertIn("PR feedback time", report)
        self.assertIn("improvement: 20.0%", report)


class TestWeightedMedian(unittest.TestCase):
    def test_weighted_median_prefers_heavy_stratum(self):
        # One value carrying weight 4 versus three values with weight 1:
        # half of the total weight (3.5 of 7) is only reached at the heavy
        # value, so it is the weighted median regardless of the count of
        # light values.
        pairs = [(10.0, 4.0), (2.0, 1.0), (3.0, 1.0), (4.0, 1.0)]
        self.assertEqual(ci_timing.weighted_median(pairs), 10.0)

    def test_weighted_median_boundary_inclusive(self):
        # Reaching exactly half the cumulative weight is sufficient.
        pairs = [(2.0, 1.0), (3.0, 1.0), (4.0, 1.0), (10.0, 3.0)]
        self.assertEqual(ci_timing.weighted_median(pairs), 4.0)

    def test_empty(self):
        self.assertIsNone(ci_timing.weighted_median([]))


class TestDuplicateCollection(unittest.TestCase):
    def test_recollect_merges_without_duplicates(self):
        run = make_run(
            7,
            1,
            "pull_request",
            "unit-integration-tests.yml",
            "2026-09-13T11:00:00Z",
            "sha7",
        )
        job = make_job(71, "test-core", "2026-09-13T11:01:00Z", "2026-09-13T11:30:00Z")
        client = FakeClient(
            "DataDog/dd-trace-go",
            [run],
            {(7, 1): [job]},
            {71: "\n".join(base_log_lines() + post_log_lines())},
        )
        with tempfile.TemporaryDirectory() as out:
            # Exercise the merge path exactly like cmd_collect does: collect
            # once, merge into an empty store, then merge the same records
            # again and verify nothing duplicates.
            existing, existing_pr, path = ci_timing.load_existing_observations(out)
            self.assertEqual(len(existing), 0)
            records = ci_timing.collect_run(client, run, "now")
            new_jobs = [r for r in records if r["kind"] == "job_observation"]
            ci_timing.write_observations(new_jobs, [], existing, path)
            first = path.read_text().strip().splitlines()
            self.assertEqual(len(first), 1)

            existing, existing_pr, path = ci_timing.load_existing_observations(out)
            self.assertEqual(len(existing), 1)
            self.assertEqual(len(existing_pr), 0)
            ci_timing.write_observations(new_jobs, [], existing, path)
            again = path.read_text().strip().splitlines()
            self.assertEqual(len(again), 1)


class TestNoCredentialsInReports(unittest.TestCase):
    def test_reports_never_carry_secrets(self):
        run = make_run(
            8,
            1,
            "pull_request",
            "unit-integration-tests.yml",
            "2026-09-13T11:00:00Z",
            "sha8",
        )
        job = make_job(81, "test-core", "2026-09-13T11:01:00Z", "2026-09-13T11:30:00Z")
        leaky_log = "\n".join(
            base_log_lines()
            + [log_line("2026-09-13T11:37:20Z", "DD_API_KEY=supersecret123")]
            + post_log_lines()
        )
        client = FakeClient(
            "DataDog/dd-trace-go", [run], {(8, 1): [job]}, {81: leaky_log}
        )
        records = ci_timing.collect_run(client, run, "now")
        blob = json.dumps(records)
        self.assertNotIn("supersecret123", blob)
        self.assertNotIn("DD_API_KEY", blob)


if __name__ == "__main__":
    unittest.main()

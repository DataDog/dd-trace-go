// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Command citiming collects CI timing and Go build-cache evidence from
// completed GitHub Actions runs and compares measurement windows.
//
// It backs the dd-trace-go Go build-cache measurement program. GitHub
// completed-run data is the acceptance source of truth; Datadog series are
// secondary.
//
// Metric definitions (these names are part of the report contract):
//
//   - job wall time: completed_at - started_at from the GitHub jobs API,
//     covering main steps and post steps of the job. Only first attempts
//     of successful jobs feed timings, whether or not the run was later
//     retried; retried runs are counted per stratum.
//   - stratum: workflow file + logical job name + runner identity (runner
//     group and sorted runner labels). Nothing the cache treatment
//     changes (workload family, resolved Go version) is part of the key.
//   - post time: log time from the first "Post job cleanup." marker to the
//     last log line; covers post steps including cache saving.
//   - restore result: classification from the structured
//     "cache-observation:" record emitted by .github/actions/setup-go:
//     exact, prefix, cold_miss, disabled, error, or unknown.
//   - save result: classification per save event from job log markers:
//     saved, exact_key_skip, conflict, error. A successful job does not prove
//     a successful save.
//   - job headline: baseline-weighted job-minutes. Each comparable stratum
//     has weight w = its baseline success count; improvement is
//     1 - sum(w*candidate p50) / sum(w*baseline p50). A stratum is
//     comparable when both windows have at least five distinct run IDs, it
//     has candidate data, and its known resolved Go version sets do not
//     differ between windows ("toolchain changed"; an unknown version
//     never excludes). The headline is refused when comparable strata
//     cover less than 80% of the baseline weight; excluded strata are
//     listed with their reason.
//   - PR feedback time: earliest workflow-run created_at for a PR revision
//     to the completion of the last non-ignored check run on that revision,
//     including queueing and the all-green delay. Human review and
//     merge-queue waiting are excluded by ignoring the same check-name
//     patterns the all-green workflow does. It is computed from every
//     pull_request run in the collection window, independent of the
//     --workflows and --events filters. Its headline uses the same
//     baseline-weighted approach per selection signature, with weight
//     w = baseline measured revisions, at least five per window, and the
//     same 80% coverage rule.
//
// Raw job logs are input data only. They are never executed, sourced, or
// republished in full; only the extracted fields above enter reports.
//
// Collection requires an authenticated `gh` with read access to workflow
// runs, jobs, logs, check runs, and the cache API; compare runs fully
// offline. Collect lists runs in hourly slices because the GitHub list-runs
// endpoint returns at most 1000 results per query, and fails if a slice
// reaches that limit. Collect is idempotent: rerunning it over an
// overlapping window merges new records and keeps existing ones. Both
// commands read the store strictly and fail on the first malformed line.
package main

import (
	"errors"
	"fmt"
	"os"
)

func main() {
	if err := dispatch(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "citiming:", err)
		os.Exit(1)
	}
}

func dispatch(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: citiming <collect|compare> [flags]")
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "collect":
		return cmdCollect(rest)
	case "compare":
		return cmdCompare(rest)
	default:
		return fmt.Errorf("unknown command %q (want collect or compare)", cmd)
	}
}

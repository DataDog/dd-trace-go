// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeClient serves canned API responses.
type fakeClient struct {
	repo   string
	runs   []run
	jobs   map[string][]job // "runID:attempt" -> jobs
	logs   map[int64]string
	checks map[string][]checkRun
	usage  string
}

func (f *fakeClient) repoPath() string { return f.repo }

func (f *fakeClient) getJSON(path string) (string, error) {
	if strings.Contains(path, "/actions/cache/usage") {
		if f.usage != "" {
			return f.usage, nil
		}
		return `{"active_caches_size_in_bytes":1024,"active_caches_count":2}`, nil
	}
	if strings.Contains(path, "/actions/caches") {
		return `{"total_count":0,"actions_caches":[]}`, nil
	}
	if strings.Contains(path, "/actions/runs?") {
		// Simulate real per-page behavior: 100 items per page so fetchAll's
		// page loop terminates exactly like it does against the live API.
		page := 1
		if _, rest, ok := strings.Cut(path, "&page="); ok {
			fmt.Sscanf(rest, "%d", &page)
		}
		start := (page - 1) * 100
		end := min(start+100, len(f.runs))
		var payload struct {
			WorkflowRuns []run `json:"workflow_runs"`
		}
		if start < len(f.runs) {
			payload.WorkflowRuns = f.runs[start:end]
		} else {
			payload.WorkflowRuns = []run{}
		}
		b, err := json.Marshal(payload)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	if strings.Contains(path, "/attempts/") && strings.Contains(path, "/jobs") {
		for key, jobs := range f.jobs {
			id, attempt, _ := strings.Cut(key, ":")
			if strings.Contains(path, "/runs/"+id+"/attempts/"+attempt+"/jobs") {
				var payload struct {
					TotalCount int   `json:"total_count"`
					Jobs       []job `json:"jobs"`
				}
				payload.Jobs = jobs
				b, err := json.Marshal(payload)
				if err != nil {
					return "", err
				}
				return string(b), nil
			}
		}
	}
	if strings.Contains(path, "/commits/") && strings.Contains(path, "/check-runs") {
		for sha, checks := range f.checks {
			if strings.Contains(path, sha) {
				var payload struct {
					TotalCount int        `json:"total_count"`
					CheckRuns  []checkRun `json:"check_runs"`
				}
				payload.CheckRuns = checks
				b, err := json.Marshal(payload)
				if err != nil {
					return "", err
				}
				return string(b), nil
			}
		}
	}
	return "", fmt.Errorf("fakeClient: unexpected path %s", path)
}

func (f *fakeClient) getLog(path string) (string, error) {
	for id, text := range f.logs {
		if strings.Contains(path, fmt.Sprintf("/jobs/%d/logs", id)) {
			return text, nil
		}
	}
	return "", fmt.Errorf("logs unavailable for %s", path)
}

// ---- fixture helpers -------------------------------------------------------

func makeRun(id int64, attempt int, event, workflow, created, headSHA string) run {
	return run{
		ID:         id,
		RunAttempt: attempt,
		Event:      event,
		Name:       workflow,
		Path:       ".github/workflows/" + workflow,
		Status:     "completed",
		Conclusion: "success",
		CreatedAt:  created,
		HeadSHA:    headSHA,
		HeadBranch: "feature",
		HTMLURL:    fmt.Sprintf("https://example/runs/%d", id),
		PullRequest: []struct {
			Number int `json:"number"`
		}{{Number: 42}},
	}
}

func makeJob(id int64, name, started, completed string) job {
	return job{
		ID:          id,
		Name:        name,
		Status:      "completed",
		Conclusion:  "success",
		StartedAt:   started,
		CompletedAt: completed,
		Labels:      []string{"ubuntu-latest"},
	}
}

func observationJSON(enabled, outcome, cacheHit string, matched *string) string {
	restore := map[string]any{
		"name":      "setup_go",
		"enabled":   enabled,
		"outcome":   outcome,
		"cache_hit": cacheHit,
	}
	if matched != nil {
		restore["cache_matched_key"] = *matched
	}
	payload := map[string]any{
		"provider": "github-cache",
		"workload": "unit-core",
		"runtime":  map[string]string{"name": "go", "requested": "1.27", "version": "1.27.1"},
		"restores": []any{restore},
	}
	b, _ := json.Marshal(payload)
	return string(b)
}

func logLine(ts, message string) string {
	return ts + " " + message
}

func baseLogLines(observation string) []string {
	lines := []string{
		logLine("2026-09-13T11:37:05.8370630Z", "runner version"),
		logLine("2026-09-13T11:37:07.1327354Z", "##[group]Run actions/cache"),
		logLine("2026-09-13T11:37:09.0478561Z", "Cache restored successfully"),
	}
	if observation != "" {
		lines = append(lines, logLine("2026-09-13T11:37:10.0000000Z", "cache-observation:"+observation))
	}
	return lines
}

func postLogLines(marker string) []string {
	return []string{
		logLine("2026-09-13T11:37:29.7644082Z", "Post job cleanup."),
		logLine("2026-09-13T11:37:30.2097642Z", marker),
		logLine("2026-09-13T11:37:30.2236184Z", "Cleaning up orphan processes"),
	}
}

const defaultSkipMarker = "Cache hit occurred on the primary key k, not saving cache."

func joinLines(lines ...[]string) string {
	var all []string
	for _, group := range lines {
		all = append(all, group...)
	}
	return strings.Join(all, "\n")
}

// ---- restore classification -------------------------------------------------

func TestClassifyRestore(t *testing.T) {
	emptyKey := ""
	oldKey := "k-1"
	cases := []struct {
		name     string
		obs      *restoreObs
		provider string
		want     string
	}{
		{"exact hit", &restoreObs{Enabled: "true", Outcome: "success", CacheHit: "true"}, "github-cache", "exact"},
		{"prefix restore", &restoreObs{Enabled: "true", Outcome: "success", CacheHit: "false", CacheMatchedKey: &oldKey}, "github-cache", "prefix"},
		{"cold miss", &restoreObs{Enabled: "true", Outcome: "success", CacheHit: "false", CacheMatchedKey: &emptyKey}, "github-cache", "cold_miss"},
		{"disabled", &restoreObs{Enabled: "false", Outcome: "success", CacheHit: "true"}, "github-cache", "disabled"},
		{"error", &restoreObs{Enabled: "true", Outcome: "failure", CacheHit: "true"}, "github-cache", "error"},
		{"no observation", nil, "github-cache", "unknown"},
		{"empty outcome", &restoreObs{Enabled: "true", Outcome: "", CacheHit: "true"}, "github-cache", "unknown"},
		{"ambiguous cache hit", &restoreObs{Enabled: "true", Outcome: "success", CacheHit: "weird"}, "github-cache", "unknown"},
		{"github miss boolean", &restoreObs{Enabled: "true", Outcome: "success", CacheHit: "false"}, "github-cache", "cold_miss"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyRestore(tc.obs, tc.provider); got != tc.want {
				t.Fatalf("classifyRestore = %q, want %q", got, tc.want)
			}
		})
	}
}

// The cloudx provider exposes only the underlying exact-hit boolean: a
// `false` is ambiguous between a prefix restore and a cold miss and must
// stay unknown until completed-log evidence classifies it.
func TestClassifyRestoreCloudx(t *testing.T) {
	cases := []struct {
		name string
		hit  string
		want string
	}{
		{"true is an exact hit", "true", "exact"},
		{"false is ambiguous", "false", "unknown"},
		{"empty is ambiguous", "", "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obs := &restoreObs{Enabled: "true", Outcome: "success", CacheHit: tc.hit}
			if got := classifyRestore(obs, "cloudx"); got != tc.want {
				t.Fatalf("classifyRestore = %q, want %q", got, tc.want)
			}
		})
	}
	t.Run("disabled", func(t *testing.T) {
		obs := &restoreObs{Enabled: "false", Outcome: "success", CacheHit: "true"}
		if got := classifyRestore(obs, "cloudx"); got != "disabled" {
			t.Fatalf("classifyRestore = %q, want disabled", got)
		}
	})
	t.Run("tools entry keeps actions/cache semantics under cloudx", func(t *testing.T) {
		// Merged observations carry the tools restore alongside the cloudx
		// build-cache restore; the tools entry must keep github semantics.
		oldKey := "k-1"
		obs := &restoreObs{Enabled: "true", Outcome: "success", CacheHit: "false", CacheMatchedKey: &oldKey}
		if got := classifyRestore(obs, "cloudx"); got != "prefix" {
			t.Fatalf("classifyRestore = %q, want prefix", got)
		}
	})
}

// ---- save classification -----------------------------------------------------

func TestClassifySaves(t *testing.T) {
	t.Run("exact key skip", func(t *testing.T) {
		log := joinLines(postLogLines(defaultSkipMarker))
		if got := classifySaves(log); len(got) != 1 || got[0] != "exact_key_skip" {
			t.Fatalf("classifySaves = %v, want [exact_key_skip]", got)
		}
	})
	t.Run("saved with key marker", func(t *testing.T) {
		log := joinLines(postLogLines("Cache saved with key: datadog-ci-cli-win-x64"))
		if got := classifySaves(log); len(got) != 1 || got[0] != "saved" {
			t.Fatalf("classifySaves = %v, want [saved]", got)
		}
	})
	t.Run("saved with the key marker", func(t *testing.T) {
		log := joinLines(postLogLines("Cache saved with the key: setup-go-macOS-arm64-go-1.27.1"))
		if got := classifySaves(log); len(got) != 1 || got[0] != "saved" {
			t.Fatalf("classifySaves = %v, want [saved]", got)
		}
	})
	t.Run("mixed save events are all recorded", func(t *testing.T) {
		log := joinLines(
			postLogLines("Cache saved with key: datadog-ci-cli-win-x64"),
			postLogLines(defaultSkipMarker),
			postLogLines("Cache saved with key: gitdb-1"),
		)
		got := classifySaves(log)
		want := []string{"saved", "exact_key_skip", "saved"}
		if len(got) != len(want) {
			t.Fatalf("classifySaves = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("classifySaves = %v, want %v", got, want)
			}
		}
	})
	t.Run("conflict", func(t *testing.T) {
		log := joinLines(postLogLines(
			"Failed to save: Unable to reserve cache with key k, another job may be creating this cache."))
		if got := classifySaves(log); len(got) != 1 || got[0] != "conflict" {
			t.Fatalf("classifySaves = %v, want [conflict]", got)
		}
	})
	t.Run("error", func(t *testing.T) {
		log := joinLines(postLogLines("Failed to save: something else"))
		if got := classifySaves(log); len(got) != 1 || got[0] != "error" {
			t.Fatalf("classifySaves = %v, want [error]", got)
		}
	})
}

// ---- log parsing --------------------------------------------------------------

func TestParseLog(t *testing.T) {
	t.Run("post seconds and observation", func(t *testing.T) {
		obs := observationJSON("true", "success", "true", nil)
		logText := joinLines(baseLogLines(obs), postLogLines(defaultSkipMarker))
		ev := parseLog(logText)
		if ev == nil {
			t.Fatal("parseLog returned nil")
		}
		if ev.postSeconds == nil || *ev.postSeconds < 0.4590 || *ev.postSeconds > 0.4593 {
			t.Fatalf("postSeconds = %v, want ~0.4592", ev.postSeconds)
		}
		if ev.observation == nil || ev.observation.Workload != "unit-core" {
			t.Fatalf("observation workload = %+v", ev.observation)
		}
		if len(ev.saves) != 1 || ev.saves[0] != "exact_key_skip" {
			t.Fatalf("saves = %v, want [exact_key_skip]", ev.saves)
		}
	})
	t.Run("BOM first line", func(t *testing.T) {
		logText := "\uFEFF" + joinLines(baseLogLines(""), postLogLines(defaultSkipMarker))
		if ev := parseLog(logText); ev == nil || ev.postSeconds == nil {
			t.Fatalf("parseLog = %+v", ev)
		}
	})
	t.Run("nanosecond log timestamps parse", func(t *testing.T) {
		// Real runner logs carry 7-digit fractions; a regression here
		// silently nulls every post-phase duration.
		logText := joinLines(baseLogLines(""), postLogLines(defaultSkipMarker))
		if ev := parseLog(logText); ev == nil || ev.postSeconds == nil {
			t.Fatal("postSeconds missing with ns-precision timestamps")
		}
	})
	t.Run("missing logs yield unknown", func(t *testing.T) {
		if parseLog("") != nil {
			t.Fatal("empty log should return nil evidence")
		}
	})
	t.Run("last observation line wins", func(t *testing.T) {
		// One job may print a partial observation first and a merged
		// superset later; the collector must keep the last one.
		runtimeObs := observationJSON("true", "success", "false", nil)
		wrapperObs := strings.Replace(observationJSON("true", "success", "true", nil),
			"unit-core", "unit-contrib", 1)
		lines := append(baseLogLines("cache-observation:"+runtimeObs),
			logLine("2026-09-13T11:37:11.0000000Z", "cache-observation:"+wrapperObs))
		logText := joinLines(lines, postLogLines(defaultSkipMarker))
		ev := parseLog(logText)
		if ev == nil || ev.observation == nil {
			t.Fatal("missing observation")
		}
		if ev.observation.Workload != "unit-contrib" {
			t.Fatalf("workload = %q, want the last (merged) record", ev.observation.Workload)
		}
	})
	t.Run("secret lines are not propagated", func(t *testing.T) {
		leak := append(baseLogLines(""),
			logLine("2026-09-13T11:37:20Z", "DD_API_KEY=deadbeef gho_abc123"))
		logText := joinLines(leak, postLogLines(defaultSkipMarker))
		ev := parseLog(logText)
		blob, _ := json.Marshal(ev)
		if strings.Contains(string(blob), "deadbeef") || strings.Contains(string(blob), "gho_abc123") {
			t.Fatal("evidence leaked secret-looking log content")
		}
	})
}

// ---- collection ----------------------------------------------------------------

func TestCollectRun(t *testing.T) {
	t.Run("job record shape", func(t *testing.T) {
		r := makeRun(1, 1, "pull_request", "unit-integration-tests.yml", "2026-09-13T11:00:00Z", "abc123")
		obs := observationJSON("true", "success", "true", nil)
		logText := joinLines(baseLogLines(obs), postLogLines(defaultSkipMarker))
		j := makeJob(11, "PR Unit and Integration Tests (1.27) / test-core",
			"2026-09-13T11:01:00Z", "2026-09-13T11:37:30Z")
		c := &fakeClient{repo: "DataDog/dd-trace-go", runs: []run{r},
			jobs: map[string][]job{"1:1": {j}}, logs: map[int64]string{11: logText}}
		records, err := collectRun(c, r, "2026-09-14T00:00:00Z")
		if err != nil {
			t.Fatal(err)
		}
		if len(records) != 1 {
			t.Fatalf("got %d records, want 1", len(records))
		}
		rec := records[0]
		if rec.Job.Seconds == nil || *rec.Job.Seconds != 2190 {
			t.Fatalf("job seconds = %v, want 2190", rec.Job.Seconds)
		}
		if rec.Job.LogicalName != "test-core" {
			t.Fatalf("logical name = %q", rec.Job.LogicalName)
		}
		if rec.Workload.Family != "unit-core" || rec.Workload.ResolvedGoVersion != "1.27.1" {
			t.Fatalf("workload = %+v", rec.Workload)
		}
		if len(rec.Cache.Restores) != 1 || rec.Cache.Restores[0].Result != "exact" {
			t.Fatalf("restores = %+v", rec.Cache.Restores)
		}
		if len(rec.Cache.Saves) != 1 || rec.Cache.Saves[0] != "exact_key_skip" {
			t.Fatalf("saves = %v, want [exact_key_skip]", rec.Cache.Saves)
		}
	})
	t.Run("rerun attempts recorded", func(t *testing.T) {
		r := makeRun(2, 2, "pull_request", "unit-integration-tests.yml", "2026-09-13T11:00:00Z", "abc123")
		failed := makeJob(21, "test-contrib-matrix (chunk 1)", "2026-09-13T11:01:00Z", "2026-09-13T11:10:00Z")
		failed.Conclusion = "failure"
		retried := makeJob(22, "test-contrib-matrix (chunk 1)", "2026-09-13T11:20:00Z", "2026-09-13T11:30:00Z")
		c := &fakeClient{repo: "DataDog/dd-trace-go", runs: []run{r},
			jobs: map[string][]job{"2:1": {failed}, "2:2": {retried}},
			logs: map[int64]string{21: joinLines(baseLogLines("")), 22: joinLines(baseLogLines(""))}}
		records, err := collectRun(c, r, "now")
		if err != nil {
			t.Fatal(err)
		}
		if len(records) != 2 {
			t.Fatalf("got %d records, want 2", len(records))
		}
		byJob := map[int64]jobObservation{}
		for _, rec := range records {
			byJob[rec.Job.ID] = rec
		}
		if byJob[21].Run.LatestAttempt {
			t.Fatal("attempt 1 must not be flagged latest")
		}
		if !byJob[22].Run.LatestAttempt {
			t.Fatal("attempt 2 must be flagged latest")
		}
		if byJob[21].Job.Conclusion != "failure" {
			t.Fatalf("attempt 1 conclusion = %q", byJob[21].Job.Conclusion)
		}
	})
	t.Run("missing logs still recorded with unknown", func(t *testing.T) {
		r := makeRun(3, 1, "push", "main-branch-tests.yml", "2026-09-13T11:00:00Z", "d")
		j := makeJob(31, "test-core", "2026-09-13T11:01:00Z", "2026-09-13T11:30:00Z")
		c := &fakeClient{repo: "DataDog/dd-trace-go", runs: []run{r},
			jobs: map[string][]job{"3:1": {j}}}
		records, err := collectRun(c, r, "now")
		if err != nil {
			t.Fatal(err)
		}
		rec := records[0]
		if rec.HasLogs {
			t.Fatal("record must mark missing logs")
		}
		if len(rec.Cache.Saves) != 0 || len(rec.Cache.Restores) != 0 {
			t.Fatalf("cache = %+v", rec.Cache)
		}
		if rec.Workload.Family != "" {
			t.Fatalf("workload family = %q", rec.Workload.Family)
		}
	})
}

func TestPagination(t *testing.T) {
	// 120 runs exercise more than one page.
	runs := make([]run, 0, 120)
	for i := range 120 {
		runs = append(runs, makeRun(int64(10+i), 1, "pull_request",
			"unit-integration-tests.yml", "2026-09-13T11:00:00Z", fmt.Sprintf("sha%d", i)))
	}
	c := &fakeClient{repo: "DataDog/dd-trace-go", runs: runs}
	// The fake returns everything in one payload; only the page loop's
	// termination is exercised here via a short list.
	seen, err := fetchAll[run](c, "repos/x/actions/runs?created=a..b", "workflow_runs")
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 120 {
		t.Fatalf("saw %d runs, want 120", len(seen))
	}
}

// ---- PR feedback -----------------------------------------------------------------

func TestCollectPRFeedback(t *testing.T) {
	t.Run("feedback and ignored checks", func(t *testing.T) {
		runs := []run{
			makeRun(1, 1, "pull_request", "all-green.yml", "2026-09-13T11:00:00Z", "sha1"),
			makeRun(2, 1, "pull_request", "unit-integration-tests.yml", "2026-09-13T11:00:10Z", "sha1"),
		}
		checks := []checkRun{
			{Name: "all-jobs-are-green", Conclusion: "success", CompletedAt: "2026-09-13T11:29:00Z"},
			{Name: "devflow/mergegate", Conclusion: "success", CompletedAt: "2026-09-13T18:00:00Z"},
			{Name: "test-core", Conclusion: "success", CompletedAt: "2026-09-13T11:20:00Z"},
		}
		c := &fakeClient{repo: "DataDog/dd-trace-go", runs: runs,
			checks: map[string][]checkRun{"sha1": checks}}
		records := collectPRFeedback(c, runs, "now")
		if len(records) != 1 {
			t.Fatalf("got %d records, want 1", len(records))
		}
		rec := records[0]
		if rec.Seconds == nil || *rec.Seconds != 29*60 {
			t.Fatalf("seconds = %v, want 1740", rec.Seconds)
		}
		if rec.ChecksCount != 2 {
			t.Fatalf("checks count = %d, want 2 (mergegate ignored)", rec.ChecksCount)
		}
	})
	t.Run("defers revisions with pending checks", func(t *testing.T) {
		// A revision enters the run list as soon as one workflow
		// finishes; feedback must wait until every non-ignored check
		// has completed so the recorded time is not understated.
		runs := []run{
			makeRun(9, 1, "pull_request", "all-green.yml", "2026-09-13T11:00:00Z", "sha9"),
		}
		checks := []checkRun{
			{Name: "all-jobs-are-green", Conclusion: "success", CompletedAt: ""},
			{Name: "test-core", Conclusion: "success", CompletedAt: "2026-09-13T11:20:00Z"},
		}
		c := &fakeClient{repo: "DataDog/dd-trace-go", runs: runs,
			checks: map[string][]checkRun{"sha9": checks}}
		if got := collectPRFeedback(c, runs, "now"); len(got) != 0 {
			t.Fatalf("deferred revision recorded %d feedback records, want 0", len(got))
		}
	})

	t.Run("signature groups identical selections", func(t *testing.T) {
		sig := shortSignature([]string{"test-core"})
		if sig == shortSignature([]string{"test-core", "other"}) {
			t.Fatal("different selections must produce different signatures")
		}
		if sig != shortSignature([]string{"test-core"}) {
			t.Fatal("identical selections must produce identical signatures")
		}
	})
}

// ---- snapshot sizes ------------------------------------------------------------------

func TestCacheSnapshotUsesAPIBytes(t *testing.T) {
	c := &fakeClient{repo: "DataDog/dd-trace-go"}
	snap, err := collectCacheSnapshot(c, "now")
	if err != nil {
		t.Fatal(err)
	}
	// size_in_bytes comes from the API untouched; no unit conversion.
	if snap.Usage.ActiveCachesSizeInBytes != 1024 {
		t.Fatalf("usage = %+v", snap.Usage)
	}
}

// ---- weighted median -----------------------------------------------------------------

func TestWeightedMedian(t *testing.T) {
	t.Run("heavy stratum wins", func(t *testing.T) {
		pairs := []weightedValue{{10, 4}, {2, 1}, {3, 1}, {4, 1}}
		got := weightedMedian(pairs)
		if got == nil || *got != 10 {
			t.Fatalf("weightedMedian = %v, want 10", got)
		}
	})
	t.Run("boundary inclusive", func(t *testing.T) {
		pairs := []weightedValue{{2, 1}, {3, 1}, {4, 1}, {10, 3}}
		got := weightedMedian(pairs)
		if got == nil || *got != 4 {
			t.Fatalf("weightedMedian = %v, want 4", got)
		}
	})
	t.Run("empty", func(t *testing.T) {
		if weightedMedian(nil) != nil {
			t.Fatal("empty input must return nil")
		}
	})
}

// ---- store dedup ----------------------------------------------------------------------

func TestStoreMergeSemantics(t *testing.T) {
	dir := t.TempDir()
	r := makeRun(7, 1, "pull_request", "unit-integration-tests.yml",
		"2026-09-13T11:00:00Z", "sha7")
	j := makeJob(71, "test-core", "2026-09-13T11:01:00Z", "2026-09-13T11:30:00Z")
	c := &fakeClient{repo: "DataDog/dd-trace-go", runs: []run{r},
		jobs: map[string][]job{"7:1": {j}},
		logs: map[int64]string{71: joinLines(baseLogLines(""), postLogLines(defaultSkipMarker))}}
	records, err := collectRun(c, r, "now")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	storePath := filepath.Join(dir, "observations.jsonl")

	merge := func() (map[jobKey]jobObservation, map[prKey]prFeedback) {
		jobs, feedback, err := loadObservations(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, rec := range records {
			jobs[jobKey{rec.Run.ID, rec.Run.Attempt, rec.Job.ID}] = rec
		}
		if err := writeStore(storePath, jobs, feedback); err != nil {
			t.Fatal(err)
		}
		return jobs, feedback
	}

	t.Run("idempotent job merge", func(t *testing.T) {
		merge()
		if got := countLines(t, storePath); got != 1 {
			t.Fatalf("first write: %d lines, want 1", got)
		}
		merge()
		if got := countLines(t, storePath); got != 1 {
			t.Fatalf("re-merge duplicated records: %d lines, want 1", got)
		}
	})

	t.Run("refreshes superseded-attempt metadata", func(t *testing.T) {
		// Attempt 1 was collected as the latest before a rerun created
		// attempt 2; a later overlapping collection re-observes it with
		// the corrected flag and the store must take the refresh.
		jobs, _, err := loadObservations(dir)
		if err != nil {
			t.Fatal(err)
		}
		key := jobKey{7, 1, 71}
		rec := jobs[key]
		rec.Run.LatestAttempt = false
		jobs[key] = rec
		if err := writeStore(storePath, jobs, nil); err != nil {
			t.Fatal(err)
		}
		jobs, _, err = loadObservations(dir)
		if err != nil {
			t.Fatal(err)
		}
		if jobs[key].Run.LatestAttempt {
			t.Fatal("stale latest-attempt flag was not refreshed")
		}
	})

	t.Run("preserves stored feedback records", func(t *testing.T) {
		sec := 1800.0
		fb := prFeedback{Kind: "pr_feedback", Schema: schemaVersion,
			PR: 7, HeadSHA: "sha7", Seconds: &sec,
			SelectionSignature: "s7"}
		jobs, feedback, err := loadObservations(dir)
		if err != nil {
			t.Fatal(err)
		}
		feedback[prKey{7, "sha7"}] = fb
		if err := writeStore(storePath, jobs, feedback); err != nil {
			t.Fatal(err)
		}
		// A later daily collection observes no feedback; the stored
		// record must survive the rewrite.
		jobs, feedback2, err := loadObservations(dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeStore(storePath, jobs, feedback2); err != nil {
			t.Fatal(err)
		}
		jobs, feedback3, err := loadObservations(dir)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := feedback3[prKey{7, "sha7"}]; !ok {
			t.Fatal("stored pr_feedback record was dropped on re-collection")
		}
		if got := countLines(t, storePath); got != 2 {
			t.Fatalf("store holds %d lines, want 2 (job + feedback)", got)
		}
	})
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// ---- compare ------------------------------------------------------------------------

func writeTestStore(t *testing.T, dir string, jobs []jobObservation, feedback []prFeedback) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	jobsMap := map[jobKey]jobObservation{}
	for _, rec := range jobs {
		jobsMap[jobKey{rec.Run.ID, rec.Run.Attempt, rec.Job.ID}] = rec
	}
	feedbackMap := map[prKey]prFeedback{}
	for _, rec := range feedback {
		feedbackMap[prKey{rec.PR, rec.HeadSHA}] = rec
	}
	if err := writeStore(filepath.Join(dir, "observations.jsonl"), jobsMap, feedbackMap); err != nil {
		t.Fatal(err)
	}
}

func jobRecord(workflow, family string, id int64, seconds *float64, conclusion string) jobObservation {
	pr := 42
	post := 30.0
	return jobObservation{
		Kind:   "job_observation",
		Schema: schemaVersion,
		Run: runMeta{
			ID: id, Attempt: 1, LatestAttempt: true, Event: "pull_request",
			Conclusion: "success", WorkflowFile: workflow, PR: &pr,
		},
		Job: jobMeta{
			ID: id * 10, LogicalName: family, Conclusion: conclusion,
			RunnerOS: "ubuntu", RunnerArch: "x64", Seconds: seconds,
		},
		Workload: workloadMeta{Family: family, ResolvedGoVersion: "1.27.1"},
		Cache: cacheMeta{
			Restores:    []restoreClassification{{Name: "setup_go", Result: "exact"}},
			Saves:       []string{"saved"},
			PostSeconds: &post,
		},
	}
}

func secondsPtr(v float64) *float64 { return new(v) }

func TestCompareFrozenStrata(t *testing.T) {
	baseDir := t.TempDir()
	candDir := t.TempDir()
	outDir := t.TempDir()

	base := make([]jobObservation, 0, 10)
	for i := range minRunSamples {
		base = append(base, jobRecord("w.yml", "A", int64(i+1), secondsPtr(100), "success"))
	}
	for i := range minRunSamples {
		base = append(base, jobRecord("w.yml", "B", int64(101+i), secondsPtr(500), "success"))
	}
	cand := make([]jobObservation, 0, 10)
	for i := range minRunSamples {
		cand = append(cand, jobRecord("w.yml", "A", int64(201+i), secondsPtr(50), "success"))
	}
	for i := range minRunSamples {
		cand = append(cand, jobRecord("w.yml", "B", int64(301+i), secondsPtr(500), "success"))
	}

	writeTestStore(t, baseDir, base, nil)
	writeTestStore(t, candDir, cand, nil)

	err := cmdCompare([]string{"--baseline", baseDir, "--candidate", candDir, "--output-dir", outDir})
	if err != nil {
		t.Fatal(err)
	}
	report, rerr := os.ReadFile(filepath.Join(outDir, "comparison.md"))
	if rerr != nil {
		t.Fatal(rerr)
	}
	text := string(report)
	for _, want := range []string{
		"baseline weighted median: 100.0 s",
		"candidate weighted median: 50.0 s",
		"improvement: 50.0%",
		"## Cache behavior",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("report missing %q:\n%s", want, text)
		}
	}
	if !strings.Contains(text, "| 30.0 |") && !strings.Contains(text, "30.0/30.0") {
		t.Fatalf("cache behavior table lacks post p50:\n%s", text)
	}
}

func TestCompareMissingStratumReported(t *testing.T) {
	baseDir := t.TempDir()
	candDir := t.TempDir()
	outDir := t.TempDir()
	base := make([]jobObservation, 0, 10)
	for i := range minRunSamples {
		base = append(base, jobRecord("w.yml", "A", int64(i+1), secondsPtr(100), "success"))
		base = append(base, jobRecord("w.yml", "C", int64(200+i), secondsPtr(100), "success"))
	}
	cand := make([]jobObservation, 0, minRunSamples)
	for i := range minRunSamples {
		cand = append(cand, jobRecord("w.yml", "A", int64(300+i), secondsPtr(50), "success"))
	}
	writeTestStore(t, baseDir, base, nil)
	writeTestStore(t, candDir, cand, nil)
	err := cmdCompare([]string{"--baseline", baseDir, "--candidate", candDir, "--output-dir", outDir})
	if err == nil {
		t.Fatal("missing candidate stratum must refuse the aggregate")
	}
	report, rerr := os.ReadFile(filepath.Join(outDir, "comparison.md"))
	if rerr != nil {
		t.Fatal(rerr)
	}
	if !strings.Contains(string(report), "Missing coverage") ||
		!strings.Contains(string(report), "C") {
		t.Fatalf("report must list missing stratum C:\n%s", report)
	}
}

func TestCompareSkippedJobsAreAbsentNotZero(t *testing.T) {
	baseDir := t.TempDir()
	candDir := t.TempDir()
	outDir := t.TempDir()
	base := []jobObservation{jobRecord("w.yml", "A", 1, secondsPtr(100), "success")}
	// A skipped candidate job contributes no duration and no zero.
	cand := []jobObservation{jobRecord("w.yml", "A", 2, nil, "skipped")}
	writeTestStore(t, baseDir, base, nil)
	writeTestStore(t, candDir, cand, nil)
	err := cmdCompare([]string{"--baseline", baseDir, "--candidate", candDir, "--output-dir", outDir})
	if err == nil {
		t.Fatal("comparison without candidate values must not be computable")
	}
	report, rerr := os.ReadFile(filepath.Join(outDir, "comparison.md"))
	if rerr != nil {
		t.Fatal(rerr)
	}
	if !strings.Contains(string(report), "| 1 | 0 |") {
		t.Fatalf("report must show candidate count 0:\n%s", report)
	}
}

func TestCompareFailuresReportedSeparately(t *testing.T) {
	baseDir := t.TempDir()
	candDir := t.TempDir()
	outDir := t.TempDir()
	base := make([]jobObservation, 0, minRunSamples+1)
	for i := range minRunSamples {
		base = append(base, jobRecord("w.yml", "A", int64(i+1), secondsPtr(100), "success"))
	}
	base = append(base, jobRecord("w.yml", "A", 900, nil, "failure"))
	cand := make([]jobObservation, 0, minRunSamples)
	for i := range minRunSamples {
		cand = append(cand, jobRecord("w.yml", "A", int64(300+i), secondsPtr(100), "success"))
	}
	writeTestStore(t, baseDir, base, nil)
	writeTestStore(t, candDir, cand, nil)
	// The stratum meets the run minimum, so the aggregate is
	// computable; the failure is still reported separately.
	if err := cmdCompare([]string{"--baseline", baseDir, "--candidate", candDir, "--output-dir", outDir}); err != nil {
		t.Fatal(err)
	}
	report, err := os.ReadFile(filepath.Join(outDir, "comparison.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(report), "Non-success outcomes") ||
		!strings.Contains(string(report), "failure") {
		t.Fatalf("report must list failures separately:\n%s", report)
	}
}

func TestComparePRFeedbackImprovement(t *testing.T) {
	baseDir := t.TempDir()
	candDir := t.TempDir()
	outDir := t.TempDir()
	mkFB := func(sha string, sec float64) prFeedback {
		return prFeedback{Kind: "pr_feedback", Schema: schemaVersion, PR: 1,
			HeadSHA: sha, Seconds: &sec, SelectionSignature: "s1"}
	}
	base := make([]jobObservation, 0, minRunSamples)
	cand := make([]jobObservation, 0, minRunSamples)
	for i := range minRunSamples {
		base = append(base, jobRecord("w.yml", "A", int64(i+1), secondsPtr(100), "success"))
		cand = append(cand, jobRecord("w.yml", "A", int64(100+i), secondsPtr(50), "success"))
	}
	baseFB := make([]prFeedback, 0, minRevisions)
	candFB := make([]prFeedback, 0, minRevisions)
	for i := range minRevisions {
		// Five distinct revisions (head SHAs) of one PR, all sharing
		// the same selection signature.
		baseFB = append(baseFB, mkFB(fmt.Sprintf("bsha%d", i), 3000))
		candFB = append(candFB, mkFB(fmt.Sprintf("csha%d", i), 2400))
	}
	writeTestStore(t, baseDir, base, baseFB)
	writeTestStore(t, candDir, cand, candFB)
	if err := cmdCompare([]string{"--baseline", baseDir, "--candidate", candDir, "--output-dir", outDir}); err != nil {
		t.Fatal(err)
	}
	report, err := os.ReadFile(filepath.Join(outDir, "comparison.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(report), "PR feedback time") ||
		!strings.Contains(string(report), "improvement: 20.0%") {
		t.Fatalf("report must show PR feedback improvement:\n%s", report)
	}
}

func TestComparePRFeedbackNeedsMeasuredRevisions(t *testing.T) {
	baseDir := t.TempDir()
	candDir := t.TempDir()
	outDir := t.TempDir()
	base := make([]jobObservation, 0, minRunSamples)
	cand := make([]jobObservation, 0, minRunSamples)
	baseFB := make([]prFeedback, 0, minRevisions)
	candFB := make([]prFeedback, 0, minRevisions)
	for i := range minRunSamples {
		base = append(base, jobRecord("w.yml", "A", int64(i+1), secondsPtr(100), "success"))
		cand = append(cand, jobRecord("w.yml", "A", int64(100+i), secondsPtr(50), "success"))
	}
	for i := range minRevisions {
		var baseSeconds, candSeconds *float64
		if i == 0 {
			baseSeconds, candSeconds = secondsPtr(3000), secondsPtr(2400)
		}
		baseFB = append(baseFB, prFeedback{Kind: "pr_feedback", Schema: schemaVersion,
			PR: 1, HeadSHA: fmt.Sprintf("base-%d", i),
			SelectionSignature: "same", Seconds: baseSeconds})
		candFB = append(candFB, prFeedback{Kind: "pr_feedback", Schema: schemaVersion,
			PR: 1, HeadSHA: fmt.Sprintf("cand-%d", i),
			SelectionSignature: "same", Seconds: candSeconds})
	}
	writeTestStore(t, baseDir, base, baseFB)
	writeTestStore(t, candDir, cand, candFB)
	if err := cmdCompare([]string{"--baseline", baseDir, "--candidate", candDir, "--output-dir", outDir}); err != nil {
		t.Fatal(err)
	}
	report, err := os.ReadFile(filepath.Join(outDir, "comparison.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(report), "feedback under-sampled: same: 1 baseline, 1 candidate revisions") ||
		!strings.Contains(string(report), "- **improvement: n/a%**") {
		t.Fatalf("missing feedback durations must refuse the feedback aggregate:\n%s", report)
	}
}

func TestCompareUnderSampledRefusesAggregate(t *testing.T) {
	// Four candidate runs against five baseline runs: below the
	// minimum, the aggregate must be refused even though the numbers
	// would otherwise compute.
	baseDir := t.TempDir()
	candDir := t.TempDir()
	outDir := t.TempDir()
	base := make([]jobObservation, 0, minRunSamples)
	for i := range minRunSamples {
		base = append(base, jobRecord("w.yml", "A", int64(i+1), secondsPtr(100), "success"))
	}
	cand := make([]jobObservation, 0, minRunSamples-1)
	for i := range minRunSamples - 1 {
		cand = append(cand, jobRecord("w.yml", "A", int64(100+i), secondsPtr(50), "success"))
	}
	writeTestStore(t, baseDir, base, nil)
	writeTestStore(t, candDir, cand, nil)
	err := cmdCompare([]string{"--baseline", baseDir, "--candidate", candDir, "--output-dir", outDir})
	if err == nil {
		t.Fatal("under-sampled window must refuse the aggregate")
	}
	report, rerr := os.ReadFile(filepath.Join(outDir, "comparison.md"))
	if rerr != nil {
		t.Fatal(rerr)
	}
	if !strings.Contains(string(report), "Under-sampled") {
		t.Fatalf("report must name the under-sampled stratum:\n%s", report)
	}
}

func TestCompareFailureOnlyStratumVisible(t *testing.T) {
	// A candidate stratum whose only record failed never enters the
	// success maps, so the failure section must iterate the union of
	// all key sets or the regression would vanish from the report.
	baseDir := t.TempDir()
	candDir := t.TempDir()
	outDir := t.TempDir()
	base := make([]jobObservation, 0, minRunSamples)
	for i := range minRunSamples {
		base = append(base, jobRecord("w.yml", "A", int64(i+1), secondsPtr(100), "success"))
	}
	cand := make([]jobObservation, 0, minRunSamples+1)
	for i := range minRunSamples {
		cand = append(cand, jobRecord("w.yml", "A", int64(100+i), secondsPtr(50), "success"))
	}
	cand = append(cand, jobRecord("w.yml", "Z", 900, nil, "cancelled"))
	writeTestStore(t, baseDir, base, nil)
	writeTestStore(t, candDir, cand, nil)
	if err := cmdCompare([]string{"--baseline", baseDir, "--candidate", candDir, "--output-dir", outDir}); err != nil {
		t.Fatal(err)
	}
	report, err := os.ReadFile(filepath.Join(outDir, "comparison.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(report), "Z") {
		t.Fatalf("failure-only stratum Z missing from report:\n%s", report)
	}
}

func TestSanitizeTimestamp(t *testing.T) {
	got := sanitizeTimestamp("2026-09-22T16:17:00Z")
	if got != "20260922T161700Z" {
		t.Fatalf("sanitizeTimestamp = %q", got)
	}
}

func TestReportsNeverCarrySecrets(t *testing.T) {
	r := makeRun(8, 1, "pull_request", "unit-integration-tests.yml", "2026-09-13T11:00:00Z", "sha8")
	j := makeJob(81, "test-core", "2026-09-13T11:01:00Z", "2026-09-13T11:30:00Z")
	leaky := append(baseLogLines(""),
		logLine("2026-09-13T11:37:20Z", "DD_API_KEY=supersecret123"))
	logText := joinLines(leaky, postLogLines(defaultSkipMarker))
	c := &fakeClient{repo: "DataDog/dd-trace-go", runs: []run{r},
		jobs: map[string][]job{"8:1": {j}}, logs: map[int64]string{81: logText}}
	records, err := collectRun(c, r, "now")
	if err != nil {
		t.Fatal(err)
	}
	blob, _ := json.Marshal(records)
	if strings.Contains(string(blob), "supersecret123") || strings.Contains(string(blob), "DD_API_KEY") {
		t.Fatal("records leaked secret-looking log content")
	}
}

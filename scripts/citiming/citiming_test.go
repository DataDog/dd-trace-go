// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeClient serves canned API responses.
type fakeClient struct {
	repo   string
	runs   []run
	jobs   map[string][]job // "runID:attempt" -> jobs
	logs   map[int64]string
	checks map[string][]checkRun
	usage  string
	// runQueries records every list-runs path, in order.
	runQueries []string
	// headSHAErr, when set, fails every head_sha run query.
	headSHAErr error
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
		f.runQueries = append(f.runQueries, path)
		// Simulate the live API: filter by the inclusive created=A..B
		// range and serve 100 items per page so fetchAll's page loop
		// terminates the same way.
		matching := []run{}
		if _, query, ok := strings.Cut(path, "head_sha="); ok {
			if f.headSHAErr != nil {
				return "", f.headSHAErr
			}
			sha, _, _ := strings.Cut(query, "&")
			for _, r := range f.runs {
				if r.HeadSHA == sha && r.Event == "pull_request" {
					matching = append(matching, r)
				}
			}
		} else {
			_, query, _ := strings.Cut(path, "created=")
			created, _, _ := strings.Cut(query, "&")
			from, to, _ := strings.Cut(created, "..")
			for _, r := range f.runs {
				if r.CreatedAt >= from && r.CreatedAt <= to {
					matching = append(matching, r)
				}
			}
		}
		page := 1
		if _, rest, ok := strings.Cut(path, "&page="); ok {
			fmt.Sscanf(rest, "%d", &page)
		}
		start := (page - 1) * 100
		end := min(start+100, len(matching))
		var payload struct {
			WorkflowRuns []run `json:"workflow_runs"`
		}
		payload.WorkflowRuns = []run{}
		if start < len(matching) {
			payload.WorkflowRuns = matching[start:end]
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
	t.Run("first attempt only, flagged retried", func(t *testing.T) {
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
		if len(records) != 1 {
			t.Fatalf("got %d records, want only the attempt-1 job", len(records))
		}
		rec := records[0]
		if rec.Job.ID != 21 || rec.Run.Attempt != 1 || rec.Job.Conclusion != "failure" {
			t.Fatalf("record = job %d attempt %d conclusion %q", rec.Job.ID, rec.Run.Attempt, rec.Job.Conclusion)
		}
		if !rec.Run.Retried {
			t.Fatal("a run with a later attempt must be flagged retried")
		}
	})
	t.Run("run without rerun is not retried", func(t *testing.T) {
		r := makeRun(4, 1, "pull_request", "unit-integration-tests.yml", "2026-09-13T11:00:00Z", "abc")
		j := makeJob(41, "test-core", "2026-09-13T11:01:00Z", "2026-09-13T11:10:00Z")
		c := &fakeClient{repo: "DataDog/dd-trace-go", runs: []run{r},
			jobs: map[string][]job{"4:1": {j}}, logs: map[int64]string{41: joinLines(baseLogLines(""))}}
		records, err := collectRun(c, r, "now")
		if err != nil {
			t.Fatal(err)
		}
		if len(records) != 1 || records[0].Run.Retried {
			t.Fatalf("records = %+v", records)
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
	seen, err := fetchAll[run](c, "repos/x/actions/runs?created=2026-09-13T00:00:00Z..2026-09-13T23:59:59Z", "workflow_runs")
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 120 {
		t.Fatalf("saw %d runs, want 120", len(seen))
	}
}

// ---- run listing ------------------------------------------------------------------

func hourRuns(first int64, hour string, n int) []run {
	runs := make([]run, 0, n)
	for i := range n {
		runs = append(runs, makeRun(first+int64(i), 1, "push", "w.yml",
			"2026-09-13T"+hour+":30:00Z", fmt.Sprintf("sha%d", first+int64(i))))
	}
	return runs
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	ts, err := time.Parse(runTimeLayout, value)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

func TestFetchRunsSlicesTheWindow(t *testing.T) {
	// 1200 runs exceed the API's 1000-result cap for one query but no
	// slice does, so every run must come back exactly once.
	all := append(hourRuns(1, "00", 600), hourRuns(10000, "01", 600)...)
	c := &fakeClient{repo: "DataDog/dd-trace-go", runs: all}
	got, slices, err := fetchRuns(c, mustTime(t, "2026-09-13T00:00:00Z"), mustTime(t, "2026-09-13T03:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1200 {
		t.Fatalf("got %d runs, want 1200", len(got))
	}
	if slices != 3 {
		t.Fatalf("got %d slices, want 3 hourly slices", slices)
	}
	wantFirst := "created=2026-09-13T00:00:00Z..2026-09-13T00:59:59Z"
	if !strings.Contains(c.runQueries[0], wantFirst) {
		t.Fatalf("first query %q lacks %q", c.runQueries[0], wantFirst)
	}
}

func TestFetchRunsQueryOmitsExcludePullRequests(t *testing.T) {
	// GitHub treats any value of exclude_pull_requests, including false, as
	// true and then returns empty pull_requests arrays, so the parameter
	// must not be sent.
	c := &fakeClient{repo: "DataDog/dd-trace-go", runs: hourRuns(1, "00", 1)}
	if _, _, err := fetchRuns(c, mustTime(t, "2026-09-13T00:00:00Z"), mustTime(t, "2026-09-13T02:00:00Z")); err != nil {
		t.Fatal(err)
	}
	if len(c.runQueries) == 0 {
		t.Fatal("no queries recorded")
	}
	for _, q := range c.runQueries {
		if strings.Contains(q, "exclude_pull_requests") {
			t.Fatalf("query %q must not send exclude_pull_requests", q)
		}
	}
}

func TestFetchRunsDedupesByID(t *testing.T) {
	r := makeRun(5, 1, "push", "w.yml", "2026-09-13T00:30:00Z", "sha")
	c := &fakeClient{repo: "DataDog/dd-trace-go", runs: []run{r, r}}
	got, _, err := fetchRuns(c, mustTime(t, "2026-09-13T00:00:00Z"), mustTime(t, "2026-09-13T01:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d runs, want 1 after dedupe", len(got))
	}
}

func TestFetchRunsFailsAtQueryLimit(t *testing.T) {
	c := &fakeClient{repo: "DataDog/dd-trace-go", runs: hourRuns(1, "00", runsQueryLimit)}
	_, _, err := fetchRuns(c, mustTime(t, "2026-09-13T00:00:00Z"), mustTime(t, "2026-09-13T01:00:00Z"))
	if err == nil || !strings.Contains(err.Error(), "2026-09-13T00:00:00Z..2026-09-13T00:59:59Z") {
		t.Fatalf("err = %v, want a truncation error naming the slice", err)
	}
}

func TestCollectWindowPRFeedbackIgnoresWorkflowFilter(t *testing.T) {
	// The PR run is outside --workflows, but PR feedback measures the whole
	// pipeline and must still be computed from it.
	prRun := makeRun(1, 1, "pull_request", "all-green.yml", "2026-09-13T11:00:00Z", "sha1")
	pushRun := makeRun(2, 1, "push", "w.yml", "2026-09-13T11:10:00Z", "sha2")
	j := makeJob(21, "test-core", "2026-09-13T11:11:00Z", "2026-09-13T11:20:00Z")
	c := &fakeClient{repo: "DataDog/dd-trace-go", runs: []run{prRun, pushRun},
		jobs: map[string][]job{"2:1": {j}}, logs: map[int64]string{21: joinLines(baseLogLines(""))},
		checks: map[string][]checkRun{"sha1": {{Name: "test-core", Conclusion: "success", CompletedAt: "2026-09-13T11:30:00Z"}}}}
	records, feedback, err := collectWindow(c, window{
		since:     mustTime(t, "2026-09-13T00:00:00Z"),
		until:     mustTime(t, "2026-09-14T00:00:00Z"),
		workflows: map[string]bool{"w.yml": true},
		events:    map[string]bool{"push": true},
	}, "now")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Run.ID != 2 {
		t.Fatalf("job records = %+v, want only the push run", records)
	}
	if len(feedback) != 1 || feedback[0].PR != 42 || feedback[0].HeadSHA != "sha1" {
		t.Fatalf("feedback = %+v, want the filtered-out PR revision", feedback)
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
	t.Run("earliest run outside the window starts the clock", func(t *testing.T) {
		// The check-runs list covers the whole revision, so the start
		// must too: sha5 has a run before the window that only the
		// head_sha query returns.
		early := makeRun(50, 1, "pull_request", "all-green.yml", "2026-09-12T23:00:00Z", "sha5")
		inWindow := makeRun(51, 1, "pull_request", "unit-integration-tests.yml", "2026-09-13T11:00:00Z", "sha5")
		checks := []checkRun{{Name: "test-core", Conclusion: "success", CompletedAt: "2026-09-13T12:00:00Z"}}
		c := &fakeClient{repo: "DataDog/dd-trace-go", runs: []run{early, inWindow},
			checks: map[string][]checkRun{"sha5": checks}}
		records := collectPRFeedback(c, []run{inWindow}, "now")
		if len(records) != 1 {
			t.Fatalf("got %d records, want 1", len(records))
		}
		rec := records[0]
		if rec.CreatedAt != early.CreatedAt {
			t.Fatalf("created = %s, want the earlier run %s", rec.CreatedAt, early.CreatedAt)
		}
		if rec.Seconds == nil || *rec.Seconds != 13*3600 {
			t.Fatalf("seconds = %v, want 46800", rec.Seconds)
		}
		if !slices.Equal(rec.RunIDs, []int64{50, 51}) {
			t.Fatalf("run ids = %v, want [50 51]", rec.RunIDs)
		}
		for _, q := range c.runQueries {
			if strings.Contains(q, "exclude_pull_requests") {
				t.Fatalf("query %q sends exclude_pull_requests", q)
			}
		}
	})
	t.Run("falls back to in-window runs when the revision query fails", func(t *testing.T) {
		inWindow := makeRun(61, 1, "pull_request", "unit-integration-tests.yml", "2026-09-13T11:00:00Z", "sha6")
		checks := []checkRun{{Name: "test-core", Conclusion: "success", CompletedAt: "2026-09-13T12:00:00Z"}}
		c := &fakeClient{repo: "DataDog/dd-trace-go", runs: []run{inWindow},
			checks:     map[string][]checkRun{"sha6": checks},
			headSHAErr: errors.New("boom")}
		records := collectPRFeedback(c, []run{inWindow}, "now")
		if len(records) != 1 || records[0].CreatedAt != inWindow.CreatedAt ||
			!slices.Equal(records[0].RunIDs, []int64{61}) {
			t.Fatalf("records = %+v, want the in-window fallback", records)
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
		jobs, feedback, err := loadStoreForUpdate(dir)
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

	t.Run("refreshes retried flag", func(t *testing.T) {
		// Attempt 1 was collected before a rerun created attempt 2; a
		// later overlapping collection re-observes it with the retried
		// flag and the store must take the refresh.
		jobs, _, err := loadStoreForUpdate(dir)
		if err != nil {
			t.Fatal(err)
		}
		key := jobKey{7, 1, 71}
		rec := jobs[key]
		rec.Run.Retried = true
		jobs[key] = rec
		if err := writeStore(storePath, jobs, nil); err != nil {
			t.Fatal(err)
		}
		jobs, _, err = loadStoreForUpdate(dir)
		if err != nil {
			t.Fatal(err)
		}
		if !jobs[key].Run.Retried {
			t.Fatal("stale retried flag was not refreshed")
		}
	})

	t.Run("preserves stored feedback records", func(t *testing.T) {
		sec := 1800.0
		fb := prFeedback{Kind: "pr_feedback", Schema: schemaVersion,
			PR: 7, HeadSHA: "sha7", Seconds: &sec,
			SelectionSignature: "s7"}
		jobs, feedback, err := loadStoreForUpdate(dir)
		if err != nil {
			t.Fatal(err)
		}
		feedback[prKey{7, "sha7"}] = fb
		if err := writeStore(storePath, jobs, feedback); err != nil {
			t.Fatal(err)
		}
		// A later daily collection observes no feedback; the stored
		// record must survive the rewrite.
		jobs, feedback2, err := loadStoreForUpdate(dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeStore(storePath, jobs, feedback2); err != nil {
			t.Fatal(err)
		}
		jobs, feedback3, err := loadStoreForUpdate(dir)
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

func TestLoadObservationsIsStrict(t *testing.T) {
	good, err := json.Marshal(jobRecord("w.yml", "A", 1, secondsPtr(10), "success"))
	if err != nil {
		t.Fatal(err)
	}
	oldSchema := strings.Replace(string(good), fmt.Sprintf(`"schema":%d`, schemaVersion), `"schema":1`, 1)
	for name, tc := range map[string]struct{ line, want string }{
		"malformed json": {`{"kind":`, "line 2"},
		"unknown kind":   {`{"kind":"other","schema":2}`, `unknown record kind "other"`},
		"old schema":     {oldSchema, "schema 1"},
		"bad record":     {fmt.Sprintf(`{"kind":"job_observation","schema":%d,"run":"x"}`, schemaVersion), "line 2"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "observations.jsonl")
			content := string(good) + "\n" + tc.line + "\n"
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			_, _, err := loadObservations(dir)
			if err == nil || !strings.Contains(err.Error(), tc.want) ||
				!strings.Contains(err.Error(), path) {
				t.Fatalf("err = %v, want it to name %s and contain %q", err, path, tc.want)
			}
			// compare shares the loader and must reject the store too.
			cmpErr := cmdCompare([]string{"--baseline", dir, "--candidate", dir, "--output-dir", t.TempDir()})
			if cmpErr == nil || !strings.Contains(cmpErr.Error(), tc.want) {
				t.Fatalf("compare err = %v, want %q", cmpErr, tc.want)
			}
			after, _ := os.ReadFile(path)
			if string(after) != content {
				t.Fatal("a rejected store must not be rewritten")
			}
		})
	}
	t.Run("missing store", func(t *testing.T) {
		_, _, err := loadObservations(t.TempDir())
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("err = %v, want fs.ErrNotExist", err)
		}
		jobs, feedback, err := loadStoreForUpdate(t.TempDir())
		if err != nil || len(jobs) != 0 || len(feedback) != 0 {
			t.Fatalf("collect must start from an empty store, got %d/%d, %v", len(jobs), len(feedback), err)
		}
	})
}

func TestWriteStoreIsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "observations.jsonl")
	if err := os.WriteFile(path, []byte("previous\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := jobRecord("w.yml", "A", 1, secondsPtr(10), "success")
	jobs := map[jobKey]jobObservation{{1, 1, 10}: rec}
	if err := writeStore(path, jobs, nil); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "observations.jsonl" {
		t.Fatalf("directory holds %v, want only the store (no temp files)", entries)
	}
	if _, _, err := loadObservations(dir); err != nil {
		t.Fatal(err)
	}
	// A failed write must leave the previous store untouched.
	if err := writeStore(filepath.Join(dir, "missing", "observations.jsonl"), jobs, nil); err == nil {
		t.Fatal("write into a missing directory must fail")
	}
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
			ID: id, Attempt: 1, Event: "pull_request",
			Conclusion: "success", WorkflowFile: workflow, PR: &pr,
		},
		Job: jobMeta{
			ID: id * 10, Name: family, Conclusion: conclusion,
			RunnerGroup: "group", RunnerLabels: []string{"ubuntu-latest", "x64"}, Seconds: seconds,
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
		// weights 5+5; 5*100+5*500 = 50.0 job-minutes vs 5*50+5*500 = 45.8.
		"covered share: 100.0%",
		"baseline weighted job-minutes: 50.0 min",
		"candidate weighted job-minutes: 45.8 min",
		"improvement: 8.3%",
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

func TestStratifyKeysOnFullJobName(t *testing.T) {
	// Reusable-workflow callers prefix the job name with the caller job,
	// which carries the matrix dimension (here the Go version).
	a := jobRecord("w.yml", "PR Unit and Integration Tests (1.26) / test-core", 1, secondsPtr(1), "success")
	b := jobRecord("w.yml", "PR Unit and Integration Tests (1.27) / test-core", 2, secondsPtr(1), "success")
	if stratify(a) == stratify(b) {
		t.Fatalf("1.26 and 1.27 callers share stratum %v", stratify(a))
	}
}

func TestCompareRunnerIdentityKeepsMarkdownTablesIntact(t *testing.T) {
	report, err := runCompare(t, []stratumSpec{{name: "A", baseSecs: 100, candSecs: 50}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report, "group (ubuntu-latest,x64)") {
		t.Fatalf("report lacks runner identity \"group (ubuntu-latest,x64)\":\n%s", report)
	}
	columns := 0
	for line := range strings.SplitSeq(report, "\n") {
		if !strings.HasPrefix(line, "|") {
			columns = 0
			continue
		}
		if n := strings.Count(line, "|"); columns == 0 {
			columns = n
		} else if n != columns {
			t.Fatalf("table row has %d pipes, want %d: %s", n, columns, line)
		}
	}
}

func TestStratifyRunnerIdentity(t *testing.T) {
	rec := jobRecord("w.yml", "A", 1, secondsPtr(1), "success")
	rec.Job.RunnerGroup = "GitHub Actions"
	rec.Job.RunnerLabels = []string{"x64", "ubuntu-latest"}
	if got := stratify(rec).runner; got != "GitHub Actions (ubuntu-latest,x64)" {
		t.Fatalf("runner = %q", got)
	}
	rec.Job.RunnerGroup = ""
	if got := stratify(rec).runner; got != "(ubuntu-latest,x64)" {
		t.Fatalf("runner without group = %q", got)
	}
}

func TestCompareCountsJobsWithoutCacheObservation(t *testing.T) {
	observed := func(id int64) jobObservation {
		rec := jobRecord("w.yml", "A", id, secondsPtr(100), "success")
		rec.Workload.Provider = "github-cache"
		return rec
	}
	unobserved := jobRecord("w.yml", "A", 3, secondsPtr(100), "success")
	unobserved.Workload = workloadMeta{Provider: "unknown"}
	unobserved.Cache = cacheMeta{}
	unobserved.HasLogs = true // logs without a cache-observation record

	split := perfValues([]jobObservation{observed(1), observed(2), unobserved})
	agg := split.cache[stratify(unobserved)]
	if agg == nil || agg.noObs != 1 || agg.restores != 2 {
		t.Fatalf("cache agg = %+v, want noObs=1 restores=2", agg)
	}

	rows := cacheBehaviorRows(split, perfValues(nil))
	if len(rows) != 1 || rows[0].baseNoObs != 1 || rows[0].candNoObs != 0 {
		t.Fatalf("cache rows = %+v, want baseNoObs=1", rows)
	}
	report := renderReport(reportData{cacheRows: rows})
	if !strings.Contains(report, "no observation b/c") || !strings.Contains(report, "| 1/0 |") {
		t.Fatalf("report lacks the no-observation column:\n%s", report)
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
		t.Fatal("50% covered share must refuse the aggregate")
	}
	report, rerr := os.ReadFile(filepath.Join(outDir, "comparison.md"))
	if rerr != nil {
		t.Fatal(rerr)
	}
	for _, want := range []string{"## Excluded strata", "/ C / ", "no candidate data",
		"covered share: 50.0%", "aggregate refused: covered share 50.0% is below the minimum of 80%"} {
		if !strings.Contains(string(report), want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}
}

// compareStrata builds a baseline and candidate store where every named
// stratum has minRunSamples successful first attempts per window, taking
// its timings and Go versions from the given specs. A stratum absent from
// the candidate map has no candidate records.
type stratumSpec struct {
	name                string
	baseSecs, candSecs  float64
	baseFamily, baseVer string
	candFamily, candVer string
	noCandidate         bool
	retried             bool
}

func runCompare(t *testing.T, specs []stratumSpec) (string, error) {
	t.Helper()
	baseDir, candDir, outDir := t.TempDir(), t.TempDir(), t.TempDir()
	var base, cand []jobObservation
	id := int64(1)
	for _, sp := range specs {
		for range minRunSamples {
			b := jobRecord("w.yml", sp.name, id, secondsPtr(sp.baseSecs), "success")
			b.Workload = workloadMeta{Family: sp.baseFamily, ResolvedGoVersion: sp.baseVer}
			b.Run.Retried = sp.retried
			base = append(base, b)
			id++
			if sp.noCandidate {
				continue
			}
			c := jobRecord("w.yml", sp.name, id, secondsPtr(sp.candSecs), "success")
			c.Workload = workloadMeta{Family: sp.candFamily, ResolvedGoVersion: sp.candVer}
			cand = append(cand, c)
			id++
		}
	}
	writeTestStore(t, baseDir, base, nil)
	writeTestStore(t, candDir, cand, nil)
	err := cmdCompare([]string{"--baseline", baseDir, "--candidate", candDir, "--output-dir", outDir})
	report, rerr := os.ReadFile(filepath.Join(outDir, "comparison.md"))
	if rerr != nil {
		t.Fatal(rerr)
	}
	return string(report), err
}

func TestCompareAggregatesOverEligibleStrata(t *testing.T) {
	// E has no candidate data: 4 of 5 equal-weight strata (80%) is the
	// minimum coverage, so the aggregate is computed over A-D only and E
	// is listed as excluded.
	specs := []stratumSpec{
		{name: "A", baseSecs: 100, candSecs: 50}, {name: "B", baseSecs: 100, candSecs: 50},
		{name: "C", baseSecs: 100, candSecs: 50}, {name: "D", baseSecs: 100, candSecs: 50},
		{name: "E", baseSecs: 100, noCandidate: true},
	}
	report, err := runCompare(t, specs)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"covered share: 80.0%", "improvement: 50.0%", "/ E / ", "no candidate data"} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}
}

func TestCompareStratumIsStableAcrossTreatment(t *testing.T) {
	// The baseline has no cache observation (family "", version ""); the
	// candidate carries a cloudx observation. Same workflow, job and
	// runner: one stratum, compared.
	report, err := runCompare(t, []stratumSpec{{
		name: "A", baseSecs: 100, candSecs: 60,
		candFamily: "orchestrion-matrix", candVer: "1.27.1",
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"| 5 | 5 |", "improvement: 40.0%"} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, "## Excluded strata") {
		t.Fatalf("stratum must not be excluded:\n%s", report)
	}
}

func TestCompareToolchainChangedIsExcluded(t *testing.T) {
	// A changes known versions and is excluded (its 10x slowdown must not
	// leak into the aggregate). B has an unknown baseline version and C an
	// unknown candidate version; unknown never excludes.
	specs := []stratumSpec{
		{name: "A", baseSecs: 100, candSecs: 1000, baseVer: "1.26.0", candVer: "1.27.1"},
		{name: "B", baseSecs: 100, candSecs: 50, candVer: "1.27.1"},
		{name: "C", baseSecs: 100, candSecs: 50, baseVer: "1.26.0"},
		{name: "D", baseSecs: 100, candSecs: 50, baseVer: "1.27.1", candVer: "1.27.1"},
		{name: "E", baseSecs: 100, candSecs: 50},
		{name: "F", baseSecs: 100, candSecs: 50},
	}
	report, err := runCompare(t, specs)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"toolchain changed (baseline 1.26.0; candidate 1.27.1)",
		"covered share: 83.3%", "improvement: 50.0%",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}
	if strings.Count(report, "toolchain changed") != 1 {
		t.Fatalf("only A may be toolchain-changed:\n%s", report)
	}
}

func TestCompareUsesFirstAttemptsAndCountsRetries(t *testing.T) {
	// Every baseline first attempt belongs to a run that was later
	// retried. Those first attempts still supply timings, and the retry
	// count is reported.
	report, err := runCompare(t, []stratumSpec{{name: "A", baseSecs: 100, candSecs: 50, retried: true}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"| 5 | 5 |", "improvement: 50.0%",
		"## Non-success outcomes and retries", "baseline 0 non-success, 5 retried runs; candidate 0 non-success, 0 retried runs"} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}
}

func TestAggregateCoverageBoundary(t *testing.T) {
	p := secondsPtr(60)
	var a aggregate
	for range 4 {
		a.add(5, true, p, p)
	}
	a.add(5, false, nil, nil)
	if a.refusal() != "" {
		t.Fatalf("80%% coverage must be accepted, got %q", a.refusal())
	}
	a.add(1, false, nil, nil)
	if a.refusal() == "" || a.improvement() != nil {
		t.Fatal("coverage below 80% must be refused")
	}
	if (aggregate{}).refusal() != "no baseline observations" {
		t.Fatal("empty aggregate must be refused")
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

func TestComparePRFeedbackCoverage(t *testing.T) {
	// Signature s2 has no candidate revisions: the feedback headline
	// covers 50% of baseline weight and is refused while the job
	// aggregate is unaffected.
	baseDir, candDir, outDir := t.TempDir(), t.TempDir(), t.TempDir()
	base := make([]jobObservation, 0, minRunSamples)
	cand := make([]jobObservation, 0, minRunSamples)
	baseFB := make([]prFeedback, 0, 2*minRevisions)
	candFB := make([]prFeedback, 0, minRevisions)
	for i := range minRunSamples {
		base = append(base, jobRecord("w.yml", "A", int64(i+1), secondsPtr(100), "success"))
		cand = append(cand, jobRecord("w.yml", "A", int64(100+i), secondsPtr(50), "success"))
	}
	mk := func(sig, sha string) prFeedback {
		return prFeedback{Kind: "pr_feedback", Schema: schemaVersion, PR: 1,
			HeadSHA: sha, Seconds: secondsPtr(3000), SelectionSignature: sig}
	}
	for i := range minRevisions {
		baseFB = append(baseFB, mk("s1", fmt.Sprintf("b1-%d", i)), mk("s2", fmt.Sprintf("b2-%d", i)))
		candFB = append(candFB, mk("s1", fmt.Sprintf("c1-%d", i)))
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
	for _, want := range []string{"aggregate refused: covered share 50.0% is below the minimum of 80%",
		"excluded signature s2: no candidate revisions"} {
		if !strings.Contains(string(report), want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
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
	if !strings.Contains(string(report), "excluded signature same: 1 baseline, 1 candidate measured revisions") ||
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
	if !strings.Contains(string(report), "below 5 distinct runs (5 baseline, 4 candidate)") {
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

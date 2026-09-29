// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// client is the GitHub API surface the collector needs. Implementations
// shell out to `gh api` for authenticated reads; tests use canned responses.
type client interface {
	getJSON(path string) (string, error)
	getLog(path string) (string, error)
	repoPath() string
}

type ghClient struct{ repo string }

func runGh(args ...string) (string, error) {
	cmd := exec.Command("gh", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		return "", fmt.Errorf("gh %s: %w: %s", args[len(args)-1], err, msg)
	}
	return stdout.String(), nil
}

func (c *ghClient) getJSON(path string) (string, error) {
	return runGh("api", "-H", "Accept: application/vnd.github+json", path)
}

// getLog fetches job logs, which contain terminal escape sequences and a
// UTF-8 BOM; gh only emits them with --allow-escape-sequences, which older
// gh versions lack, so fall back to a plain call.
func (c *ghClient) getLog(path string) (string, error) {
	out, err := runGh("api", "--allow-escape-sequences", path)
	if err != nil {
		out, err = runGh("api", path)
	}
	return out, err
}

// repoPath returns the repository segment for API URLs.
func (c *ghClient) repoPath() string { return c.repo }

// fetchAll walks a list endpoint with per_page=100 pagination. Different
// endpoints wrap their arrays under different keys (workflow_runs, jobs,
// check_runs, actions_caches), so the key is passed in.
func fetchAll[T any](c client, path, key string) ([]T, error) {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	var all []T
	for page := 1; ; page++ {
		body, err := c.getJSON(fmt.Sprintf("%s%sper_page=100&page=%d", path, sep, page))
		if err != nil {
			return nil, err
		}
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal([]byte(body), &envelope); err != nil {
			return nil, fmt.Errorf("decode %s page %d: %w", path, page, err)
		}
		raw, ok := envelope[key]
		if !ok {
			return nil, fmt.Errorf("response of %s has no %q array", path, key)
		}
		var items []T
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, fmt.Errorf("decode %s items: %w", path, err)
		}
		all = append(all, items...)
		if len(items) < 100 {
			return all, nil
		}
	}
}

// run is a GitHub workflow run as returned by the runs list API.
type run struct {
	ID          int64  `json:"id"`
	RunAttempt  int    `json:"run_attempt"`
	Event       string `json:"event"`
	Name        string `json:"name"`
	Path        string `json:"path"`
	Status      string `json:"status"`
	Conclusion  string `json:"conclusion"`
	CreatedAt   string `json:"created_at"`
	HeadSHA     string `json:"head_sha"`
	HeadBranch  string `json:"head_branch"`
	HTMLURL     string `json:"html_url"`
	PullRequest []struct {
		Number int `json:"number"`
	} `json:"pull_requests"`
}

// job is a workflow job as returned by the jobs API.
type job struct {
	ID              int64     `json:"id"`
	Name            string    `json:"name"`
	Status          string    `json:"status"`
	Conclusion      string    `json:"conclusion"`
	StartedAt       string    `json:"started_at"`
	CompletedAt     string    `json:"completed_at"`
	Labels          []string  `json:"labels"`
	RunnerGroupName string    `json:"runner_group_name"`
	Steps           []jobStep `json:"steps"`
}

type jobStep struct {
	Name        string `json:"name"`
	Number      int    `json:"number"`
	Status      string `json:"status"`
	Conclusion  string `json:"conclusion"`
	StartedAt   string `json:"started_at"`
	CompletedAt string `json:"completed_at"`
}

type checkRun struct {
	Name        string `json:"name"`
	Conclusion  string `json:"conclusion"`
	CompletedAt string `json:"completed_at"`
}

type cacheEntry struct {
	ID             int64  `json:"id"`
	Key            string `json:"key"`
	Ref            string `json:"ref"`
	SizeInBytes    int64  `json:"size_in_bytes"`
	CreatedAt      string `json:"created_at"`
	LastAccessedAt string `json:"last_accessed_at"`
}

// The observation records written to observations.jsonl.

type runMeta struct {
	ID           int64  `json:"id"`
	Attempt      int    `json:"attempt"`
	Retried      bool   `json:"retried"` // the run has a later attempt
	Event        string `json:"event"`
	Status       string `json:"status"`
	Conclusion   string `json:"conclusion"`
	Workflow     string `json:"workflow"`
	WorkflowFile string `json:"workflow_file"`
	CreatedAt    string `json:"created_at"`
	HeadSHA      string `json:"head_sha"`
	HeadBranch   string `json:"head_branch"`
	PR           *int   `json:"pr"`
	URL          string `json:"url"`
}

type jobMeta struct {
	ID           int64    `json:"id"`
	Name         string   `json:"name"`
	LogicalName  string   `json:"logical_name"`
	Status       string   `json:"status"`
	Conclusion   string   `json:"conclusion"`
	StartedAt    string   `json:"started_at"`
	CompletedAt  string   `json:"completed_at"`
	Seconds      *float64 `json:"seconds"`
	RunnerGroup  string   `json:"runner_group"`
	RunnerLabels []string `json:"runner_labels"`
}

type workloadMeta struct {
	Provider           string `json:"provider"`
	Family             string `json:"family"`
	RequestedGoVersion string `json:"requested_go_version"`
	ResolvedGoVersion  string `json:"resolved_go_version"`
}

type cacheMeta struct {
	Restores    []restoreClassification `json:"restores"`
	Saves       []string                `json:"saves"`
	PostSeconds *float64                `json:"post_seconds"`
}

type stepRecord struct {
	Name       string   `json:"name"`
	Number     int      `json:"number"`
	Conclusion string   `json:"conclusion"`
	Seconds    *float64 `json:"seconds"`
}

type jobObservation struct {
	Kind        string       `json:"kind"`
	Schema      int          `json:"schema"`
	CollectedAt string       `json:"collected_at"`
	Run         runMeta      `json:"run"`
	Job         jobMeta      `json:"job"`
	Workload    workloadMeta `json:"workload"`
	Cache       cacheMeta    `json:"cache"`
	Steps       []stepRecord `json:"steps"`
	HasLogs     bool         `json:"has_logs"`
}

type prFeedback struct {
	Kind                 string   `json:"kind"`
	Schema               int      `json:"schema"`
	CollectedAt          string   `json:"collected_at"`
	PR                   int      `json:"pr"`
	HeadSHA              string   `json:"head_sha"`
	CreatedAt            string   `json:"created_at"`
	LastCheckCompletedAt string   `json:"last_check_completed_at"`
	Seconds              *float64 `json:"seconds"`
	SelectionSignature   string   `json:"selection_signature"`
	ChecksCount          int      `json:"checks_count"`
	RunIDs               []int64  `json:"run_ids"`
}

type cacheUsage struct {
	ActiveCachesSizeInBytes int64 `json:"active_caches_size_in_bytes"`
	ActiveCachesCount       int   `json:"active_caches_count"`
}

type cacheSnapshot struct {
	Kind        string       `json:"kind"`
	Schema      int          `json:"schema"`
	CollectedAt string       `json:"collected_at"`
	Usage       cacheUsage   `json:"usage"`
	Entries     []cacheEntry `json:"entries"`
}

func workflowFile(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}

func logicalJobName(name string) string {
	if i := strings.LastIndex(name, " / "); i >= 0 {
		return name[i+3:]
	}
	return name
}

func isIgnoredCheck(name string) bool {
	for _, pattern := range ignoredCheckPatterns {
		if strings.Contains(name, pattern) {
			return true
		}
	}
	return false
}

// collectRun produces job observations for the first attempt of one
// workflow run. Timing and non-success counts use first attempts only, so
// later attempts are not collected; a run with a later attempt is flagged
// Retried so the report can count retries.
func collectRun(c client, r run, collectedAt string) ([]jobObservation, error) {
	path := fmt.Sprintf("repos/%s/actions/runs/%d/attempts/1/jobs", c.repoPath(), r.ID)
	jobs, err := fetchAll[job](c, path, "jobs")
	if err != nil {
		fmt.Fprintf(os.Stderr, "  ! run %d: jobs unavailable: %v\n", r.ID, err)
		return nil, nil
	}
	records := make([]jobObservation, 0, len(jobs))
	for _, j := range jobs {
		records = append(records, buildJobRecord(c, r, j, collectedAt))
	}
	return records, nil
}

// buildJobRecord assembles one observation, fetching the job's log for
// cache evidence. Missing logs keep the record with unknown classifications.
func buildJobRecord(c client, r run, j job, collectedAt string) jobObservation {
	logText, logErr := c.getLog(fmt.Sprintf("repos/%s/actions/jobs/%d/logs", c.repoPath(), j.ID))
	var ev *logEvidence
	if logErr == nil {
		ev = parseLog(logText)
	}

	var restores []restoreClassification
	wl := workloadMeta{Provider: "unknown"}
	if ev != nil && ev.observation != nil {
		for _, ro := range ev.observation.Restores {
			result := classifyRestore(&ro, ev.observation.Provider)
			restores = append(restores, restoreClassification{Name: ro.Name, Result: result})
		}
		wl = workloadMeta{
			Provider:           ev.observation.Provider,
			Family:             ev.observation.Workload,
			RequestedGoVersion: ev.observation.Runtime.Requested,
			ResolvedGoVersion:  ev.observation.Runtime.Version,
		}
		if wl.Provider == "" {
			wl.Provider = "unknown"
		}
	}

	steps := make([]stepRecord, 0, len(j.Steps))
	for _, s := range j.Steps {
		steps = append(steps, stepRecord{
			Name:       s.Name,
			Number:     s.Number,
			Conclusion: s.Conclusion,
			Seconds:    secondsBetween(s.StartedAt, s.CompletedAt),
		})
	}

	var pr *int
	if len(r.PullRequest) > 0 {
		pr = &r.PullRequest[0].Number
	}

	cache := cacheMeta{Restores: restores}
	if ev != nil {
		cache.Saves = ev.saves
		cache.PostSeconds = ev.postSeconds
	}

	return jobObservation{
		Kind:        "job_observation",
		Schema:      schemaVersion,
		CollectedAt: collectedAt,
		Run: runMeta{
			ID:           r.ID,
			Attempt:      1,
			Retried:      r.RunAttempt > 1,
			Event:        r.Event,
			Status:       r.Status,
			Conclusion:   r.Conclusion,
			Workflow:     r.Name,
			WorkflowFile: workflowFile(r.Path),
			CreatedAt:    r.CreatedAt,
			HeadSHA:      r.HeadSHA,
			HeadBranch:   r.HeadBranch,
			PR:           pr,
			URL:          r.HTMLURL,
		},
		Job: jobMeta{
			ID:           j.ID,
			Name:         j.Name,
			LogicalName:  logicalJobName(j.Name),
			Status:       j.Status,
			Conclusion:   j.Conclusion,
			StartedAt:    j.StartedAt,
			CompletedAt:  j.CompletedAt,
			Seconds:      secondsBetween(j.StartedAt, j.CompletedAt),
			RunnerGroup:  j.RunnerGroupName,
			RunnerLabels: j.Labels,
		},
		Workload: wl,
		Cache:    cache,
		Steps:    steps,
		HasLogs:  ev != nil,
	}
}

// collectPRFeedback produces one feedback record per (PR, head SHA):
// earliest workflow creation to last non-ignored check completion.
func collectPRFeedback(c client, runs []run, collectedAt string) []prFeedback {
	type revision struct {
		pr  int
		sha string
	}
	groups := map[revision][]run{}
	for _, r := range runs {
		if r.Event != "pull_request" || len(r.PullRequest) == 0 {
			continue
		}
		key := revision{r.PullRequest[0].Number, r.HeadSHA}
		groups[key] = append(groups[key], r)
	}

	keys := make([]revision, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b revision) int {
		if a.pr != b.pr {
			return a.pr - b.pr
		}
		return strings.Compare(a.sha, b.sha)
	})

	records := make([]prFeedback, 0, len(keys))
	for _, key := range keys {
		group := groups[key]
		checks, err := fetchAll[checkRun](
			c, fmt.Sprintf("repos/%s/commits/%s/check-runs", c.repoPath(), key.sha), "check_runs")
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ! pr %d %s: check-runs unavailable: %v\n", key.pr, key.sha, err)
			continue
		}
		relevant := make([]checkRun, 0, len(checks))
		for _, chk := range checks {
			if !isIgnoredCheck(chk.Name) {
				relevant = append(relevant, chk)
			}
		}
		// Defer revisions whose checks have not all completed: a
		// revision enters the run list as soon as any workflow
		// finishes, while later checks may still be pending. Freezing
		// the feedback time now would understate it; the next daily
		// collection retries the revision.
		complete := true
		for _, chk := range relevant {
			if chk.CompletedAt == "" {
				complete = false
				break
			}
		}
		if !complete {
			continue
		}
		var created, last string
		for _, r := range group {
			if created == "" || (r.CreatedAt != "" && r.CreatedAt < created) {
				created = r.CreatedAt
			}
		}
		for _, chk := range relevant {
			if chk.CompletedAt > last {
				last = chk.CompletedAt
			}
		}
		names := make([]string, 0, len(relevant))
		for _, chk := range relevant {
			names = append(names, chk.Name)
		}
		signature := shortSignature(names)

		runIDs := make(map[int64]bool, len(group))
		for _, r := range group {
			runIDs[r.ID] = true
		}
		ids := make([]int64, 0, len(runIDs))
		for id := range runIDs {
			ids = append(ids, id)
		}
		slices.Sort(ids)

		fb := prFeedback{
			Kind:               "pr_feedback",
			Schema:             schemaVersion,
			CollectedAt:        collectedAt,
			PR:                 key.pr,
			HeadSHA:            key.sha,
			CreatedAt:          created,
			ChecksCount:        len(relevant),
			SelectionSignature: signature,
			RunIDs:             ids,
			Seconds:            secondsBetween(created, last),
		}
		fb.LastCheckCompletedAt = last
		records = append(records, fb)
	}
	return records
}

// shortSignature hashes a sorted check-name set so PRs with the same
// workload selection (same ciselect gates) compare with each other.
func shortSignature(names []string) string {
	sorted := slices.Clone(names)
	slices.Sort(sorted)
	sum := sha256.Sum256([]byte(strings.Join(sorted, "\n")))
	return hex.EncodeToString(sum[:])[:16]
}

// collectCacheSnapshot captures usage and entry inventory for the day.
// Sizes come from the cache API's size_in_bytes untouched: they are the
// compressed cache-service storage, not filesystem usage.
func collectCacheSnapshot(c client, collectedAt string) (cacheSnapshot, error) {
	body, err := c.getJSON(fmt.Sprintf("repos/%s/actions/cache/usage", c.repoPath()))
	if err != nil {
		return cacheSnapshot{}, err
	}
	var usage cacheUsage
	if err := json.Unmarshal([]byte(body), &usage); err != nil {
		return cacheSnapshot{}, fmt.Errorf("decode cache usage: %w", err)
	}
	entries, err := fetchAll[cacheEntry](
		c, fmt.Sprintf("repos/%s/actions/caches", c.repoPath()), "actions_caches")
	if err != nil {
		return cacheSnapshot{}, err
	}
	return cacheSnapshot{
		Kind:        "cache_snapshot",
		Schema:      schemaVersion,
		CollectedAt: collectedAt,
		Usage:       usage,
		Entries:     entries,
	}, nil
}

// dedup keys for the observation store.
type jobKey struct {
	runID   int64
	attempt int
	jobID   int64
}

type prKey struct {
	pr  int
	sha string
}

const schemaVersion = 2

// loadObservations reads a store strictly, for both collect and compare:
// job observations by dedup key and PR feedback by revision. Any non-blank
// line that is not a well-formed record of the current schema is an error
// naming the file and line, so a damaged store is never silently dropped
// or rewritten. A missing file returns an error satisfying
// errors.Is(err, fs.ErrNotExist).
func loadObservations(dir string) (map[jobKey]jobObservation, map[prKey]prFeedback, error) {
	path := filepath.Join(dir, "observations.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	jobs := map[jobKey]jobObservation{}
	feedback := map[prKey]prFeedback{}
	for i, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var probe struct {
			Kind   string `json:"kind"`
			Schema int    `json:"schema"`
		}
		if err := json.Unmarshal([]byte(line), &probe); err != nil {
			return nil, nil, fmt.Errorf("%s line %d: %w", path, i+1, err)
		}
		if probe.Schema != schemaVersion {
			return nil, nil, fmt.Errorf("%s line %d: schema %d, want %d",
				path, i+1, probe.Schema, schemaVersion)
		}
		switch probe.Kind {
		case "job_observation":
			var rec jobObservation
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				return nil, nil, fmt.Errorf("%s line %d: %w", path, i+1, err)
			}
			jobs[jobKey{rec.Run.ID, rec.Run.Attempt, rec.Job.ID}] = rec
		case "pr_feedback":
			var rec prFeedback
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				return nil, nil, fmt.Errorf("%s line %d: %w", path, i+1, err)
			}
			feedback[prKey{rec.PR, rec.HeadSHA}] = rec
		default:
			return nil, nil, fmt.Errorf("%s line %d: unknown record kind %q",
				path, i+1, probe.Kind)
		}
	}
	return jobs, feedback, nil
}

// loadStoreForUpdate is loadObservations for collect, where a missing
// store is an empty one. Any other failure, including a malformed line,
// stops the collection before the store is rewritten.
func loadStoreForUpdate(dir string) (map[jobKey]jobObservation, map[prKey]prFeedback, error) {
	jobs, feedback, err := loadObservations(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return map[jobKey]jobObservation{}, map[prKey]prFeedback{}, nil
	}
	return jobs, feedback, err
}

// writeStore serializes the merged observation store: job
// observations keyed by (run, attempt, job) and PR feedback keyed by
// (PR, head SHA). Both maps are the complete merged state; callers
// merge freshly collected records over previously stored ones so an
// overlapping collection refreshes re-observed records (correcting,
// for example, a run's retried flag after a rerun) instead of
// dropping or duplicating them. The file is replaced atomically.
func writeStore(path string, jobs map[jobKey]jobObservation,
	feedback map[prKey]prFeedback) error {

	jobKeys := make([]jobKey, 0, len(jobs))
	for k := range jobs {
		jobKeys = append(jobKeys, k)
	}
	slices.SortFunc(jobKeys, func(a, b jobKey) int {
		return cmp.Or(cmp.Compare(a.runID, b.runID),
			cmp.Compare(a.attempt, b.attempt),
			cmp.Compare(a.jobID, b.jobID))
	})
	fbKeys := make([]prKey, 0, len(feedback))
	for k := range feedback {
		fbKeys = append(fbKeys, k)
	}
	slices.SortFunc(fbKeys, func(a, b prKey) int {
		return cmp.Or(cmp.Compare(a.pr, b.pr), cmp.Compare(a.sha, b.sha))
	})
	var buf bytes.Buffer
	for _, k := range jobKeys {
		line, err := json.Marshal(jobs[k])
		if err != nil {
			return err
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	for _, k := range fbKeys {
		line, err := json.Marshal(feedback[k])
		if err != nil {
			return err
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return writeFileAtomic(path, buf.Bytes())
}

// writeFileAtomic writes data to a temporary file in the target directory
// and renames it over path, so an interrupted write never leaves a
// truncated store.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func flagSet(name string) *flag.FlagSet {
	return flag.NewFlagSet(name, flag.ContinueOnError)
}

// Runs are listed in time slices because the list-runs endpoint returns at
// most runsQueryLimit results per query when filtered by created or status.
const (
	runsSlice      = time.Hour
	runsQueryLimit = 1000
	runTimeLayout  = "2006-01-02T15:04:05Z"
)

// fetchRuns lists completed runs created in [since, until) one slice at a
// time and dedupes them by run ID. A slice that reaches runsQueryLimit may
// have been truncated by the API, so it is an error rather than a silent
// undercount. It also returns the number of slices queried.
func fetchRuns(c client, since, until time.Time) ([]run, int, error) {
	var runs []run
	seen := map[int64]bool{}
	nSlices := 0
	for start := since; start.Before(until); start = start.Add(runsSlice) {
		end := start.Add(runsSlice)
		if end.After(until) {
			end = until
		}
		// created= ranges are inclusive at both ends.
		created := start.Format(runTimeLayout) + ".." + end.Add(-time.Second).Format(runTimeLayout)
		found, err := fetchAll[run](c,
			fmt.Sprintf("repos/%s/actions/runs?created=%s&status=completed",
				c.repoPath(), created),
			"workflow_runs")
		if err != nil {
			return nil, nSlices, err
		}
		nSlices++
		if len(found) >= runsQueryLimit {
			return nil, nSlices, fmt.Errorf(
				"runs created %s returned %d results, the API limit per query; results may be truncated",
				created, len(found))
		}
		for _, r := range found {
			if !seen[r.ID] {
				seen[r.ID] = true
				runs = append(runs, r)
			}
		}
	}
	return runs, nSlices, nil
}

// window selects what collectWindow gathers. The filters restrict which
// runs contribute job observations; PR feedback always uses every
// pull_request run in the time window, because it measures the whole
// pipeline and must not depend on which workflows are being inspected.
type window struct {
	since, until time.Time // [since, until)
	workflows    map[string]bool
	events       map[string]bool
	verbose      bool
}

func collectWindow(c client, w window, collectedAt string) ([]jobObservation, []prFeedback, error) {
	runs, nSlices, err := fetchRuns(c, w.since, w.until)
	if err != nil {
		return nil, nil, err
	}
	kept := make([]run, 0, len(runs))
	for _, r := range runs {
		if w.workflows != nil && !w.workflows[workflowFile(r.Path)] {
			continue
		}
		if w.events != nil && !w.events[r.Event] {
			continue
		}
		kept = append(kept, r)
	}
	fmt.Fprintf(os.Stdout, "Collected %d completed runs (%d kept after filters) in %d hourly slices\n",
		len(runs), len(kept), nSlices)

	var records []jobObservation
	for _, r := range kept {
		if w.verbose {
			fmt.Fprintf(os.Stdout, "  run %d %s attempt %d\n", r.ID, r.Name, r.RunAttempt)
		}
		recs, err := collectRun(c, r, collectedAt)
		if err != nil {
			return nil, nil, err
		}
		records = append(records, recs...)
	}
	feedback := collectPRFeedback(c, runs, collectedAt)
	fmt.Fprintf(os.Stdout, "  %d job observations, %d pr feedback records\n",
		len(records), len(feedback))
	return records, feedback, nil
}

func cmdCollect(args []string) error {
	flags := flagSet("collect")
	repo := flags.String("repo", "", "OWNER/NAME")
	since := flags.String("since", "", "YYYY-MM-DD (inclusive)")
	until := flags.String("until", "", "YYYY-MM-DD (inclusive)")
	outputDir := flags.String("output-dir", "", "directory for evidence files")
	workflows := flags.String("workflows", "", "comma-separated workflow file names to keep")
	events := flags.String("events", "", "comma-separated event names to keep")
	snapshot := flags.Bool("cache-snapshot", false, "also snapshot cache usage and entries")
	verbose := flags.Bool("verbose", false, "print each run as it is collected")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *repo == "" || *since == "" || *until == "" || *outputDir == "" {
		return errors.New("collect needs --repo, --since, --until and --output-dir")
	}
	sinceDay, err := time.Parse(time.DateOnly, *since)
	if err != nil {
		return fmt.Errorf("--since: %w", err)
	}
	untilDay, err := time.Parse(time.DateOnly, *until)
	if err != nil {
		return fmt.Errorf("--until: %w", err)
	}
	w := window{since: sinceDay, until: untilDay.AddDate(0, 0, 1), verbose: *verbose}
	if *workflows != "" {
		w.workflows = splitSet(*workflows)
	}
	if *events != "" {
		w.events = splitSet(*events)
	}

	c := &ghClient{repo: *repo}
	collectedAt := nowUTC()
	if err := os.MkdirAll(*outputDir, 0o755); err != nil {
		return err
	}
	records, feedback, err := collectWindow(c, w, collectedAt)
	if err != nil {
		return err
	}

	existingJobs, existingFeedback, err := loadStoreForUpdate(*outputDir)
	if err != nil {
		return err
	}
	added, refreshed := 0, 0
	for _, rec := range records {
		key := jobKey{rec.Run.ID, rec.Run.Attempt, rec.Job.ID}
		if _, ok := existingJobs[key]; ok {
			refreshed++
		} else {
			added++
		}
		existingJobs[key] = rec
	}
	fbAdded, fbRefreshed := 0, 0
	for _, rec := range feedback {
		key := prKey{rec.PR, rec.HeadSHA}
		if _, ok := existingFeedback[key]; ok {
			fbRefreshed++
		} else {
			fbAdded++
		}
		existingFeedback[key] = rec
	}
	storePath := filepath.Join(*outputDir, "observations.jsonl")
	if err := writeStore(storePath, existingJobs, existingFeedback); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout,
		"  store: %d added, %d refreshed; feedback %d added, %d refreshed -> %s\n",
		added, refreshed, fbAdded, fbRefreshed, storePath)

	if *snapshot {
		snap, err := collectCacheSnapshot(c, collectedAt)
		if err != nil {
			return err
		}
		snapPath := *outputDir + "/cache_snapshot_" +
			sanitizeTimestamp(collectedAt) + ".json"
		data, err := json.MarshalIndent(snap, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(snapPath, append(data, '\n'), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "  cache snapshot: %d entries, %d bytes active -> %s\n",
			snap.Usage.ActiveCachesCount, snap.Usage.ActiveCachesSizeInBytes, snapPath)
	}

	return nil
}

// sanitizeTimestamp renders a collection timestamp as a filename-safe
// stamp, e.g. 2026-09-22T16:17:00Z -> 20260922T161700Z, so daily
// snapshots do not overwrite each other and a week keeps every day's
// inventory.
func sanitizeTimestamp(ts string) string {
	return strings.Map(func(r rune) rune {
		if r == '-' || r == ':' {
			return -1
		}
		return r
	}, ts)
}

func splitSet(value string) map[string]bool {
	set := map[string]bool{}
	for part := range strings.SplitSeq(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			set[part] = true
		}
	}
	return set
}

func nowUTC() string {
	return timeNow().UTC().Format("2006-01-02T15:04:05Z")
}

// timeNow is a seam for tests.
var timeNow = time.Now

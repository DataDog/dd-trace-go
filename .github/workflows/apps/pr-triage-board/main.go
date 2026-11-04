// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Command pr-triage-board renders a markdown triage board of the open pull
// requests of one or more GitHub repositories. It can write the board to a
// file, to stdout or to the body of a GitHub issue.
//
// Usage:
//
//	GITHUB_TOKEN=... go run ./.github/workflows/apps/pr-triage-board \
//	    --repo DataDog/dd-trace-go --output /tmp/board.md
//
// See classify's doc comment for the bucket rules.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"slices"
	"strings"
	"time"
)

const (
	defaultRepo  = "DataDog/dd-trace-go"
	defaultLimit = 100

	// maxIssueBodyChars is the size limit of a GitHub issue body.
	maxIssueBodyChars = 65536
)

type config struct {
	repos             []string
	limit             int
	maintainer        string
	issue             int
	output            string
	classificationOut string
}

// repoFlag collects the values of a repeatable flag.
type repoFlag []string

func (f *repoFlag) String() string { return strings.Join(*f, ",") }

func (f *repoFlag) Set(v string) error {
	*f = append(*f, v)
	return nil
}

func main() {
	var repos repoFlag
	cfg := config{}
	flag.Var(&repos, "repo", "repository as owner/name, repeat to aggregate (default "+defaultRepo+")")
	flag.IntVar(&cfg.limit, "limit", defaultLimit, "most recently active open PRs to inspect per repository")
	flag.StringVar(&cfg.maintainer, "maintainer", "", "only show PRs where this handle is a current owner or a CODEOWNERS owner of the changed files")
	flag.IntVar(&cfg.issue, "issue", 0, "number of the issue in the first repository whose body receives the board")
	flag.StringVar(&cfg.output, "output", "", "file for the markdown board (default stdout)")
	flag.StringVar(&cfg.classificationOut, "classification-request-out", "", "file for the JSON array of per-PR data for a classification model")
	flag.Parse()
	cfg.repos = repos

	token := os.Getenv("GITHUB_TOKEN") //nolint:forbidigo // this standalone CI program cannot import internal/env
	if token == "" {
		fatalf("GITHUB_TOKEN is not set")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, cfg, newClient(token), os.Stdout, time.Now().UTC()); err != nil {
		fatalf("%v", err)
	}
}

// logOutput receives the progress messages.
var logOutput io.Writer = os.Stderr

func logf(format string, args ...any) {
	fmt.Fprintf(logOutput, "pr-triage-board: "+format+"\n", args...)
}

func fatalf(format string, args ...any) {
	logf(format, args...)
	os.Exit(1)
}

// run gathers the pull requests, writes the outputs and updates the issue.
func run(ctx context.Context, cfg config, api *client, stdout io.Writer, now time.Time) error {
	if len(cfg.repos) == 0 {
		cfg.repos = []string{defaultRepo}
	}
	cfg.repos = dedupeStable(cfg.repos)
	for _, repo := range cfg.repos {
		if _, _, err := splitRepo(repo); err != nil {
			return err
		}
	}
	if cfg.limit < 1 {
		return fmt.Errorf("--limit must be at least 1, got %d", cfg.limit)
	}

	// GitHub evaluates ownership on the base branch of a pull request, so the
	// owners come from a CODEOWNERS file per repository and base branch.
	type ownersKey struct{ repo, baseRef string }
	ownersCache := make(map[ownersKey]*codeowners)

	var items []Item
	totalOpen := make(map[string]int)
	for _, repo := range cfg.repos {
		var err error
		if totalOpen[repo], err = api.openPullCount(ctx, repo); err != nil {
			return fmt.Errorf("count open pull requests of %s: %w", repo, err)
		}
		prs, err := api.gatherRepo(ctx, repo, cfg.limit)
		if err != nil {
			return err
		}
		for _, pr := range prs {
			key := ownersKey{repo, pr.BaseRef}
			co, ok := ownersCache[key]
			if !ok {
				if co, err = api.fetchCodeowners(ctx, repo, pr.BaseRef); err != nil {
					return err
				}
				ownersCache[key] = co
			}
			items = append(items, newItem(pr, co))
		}
		logf("%s: analyzed %d of %d open PRs", repo, len(prs), totalOpen[repo])
	}
	analyzed := len(items)
	if cfg.maintainer != "" {
		items = filterByMaintainer(items, cfg.maintainer)
	}

	board := renderBoard(items, boardOptions{
		Repos:      cfg.repos,
		TotalOpen:  totalOpen,
		Analyzed:   analyzed,
		Limit:      cfg.limit,
		Now:        now,
		Command:    cfg.command(),
		Maintainer: cfg.maintainer,
	})

	if cfg.output == "" {
		if _, err := io.WriteString(stdout, board); err != nil {
			return err
		}
	} else if err := os.WriteFile(cfg.output, []byte(board), 0o644); err != nil {
		return err
	}
	if cfg.classificationOut != "" {
		data, err := json.MarshalIndent(classificationRequest(items), "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(cfg.classificationOut, append(data, '\n'), 0o644); err != nil {
			return err
		}
	}
	if cfg.issue > 0 {
		return updateIssue(ctx, api, cfg.repos[0], cfg.issue, board)
	}
	return nil
}

// command returns the command line that reproduces the board contents.
func (c config) command() string {
	args := []string{"go run ./.github/workflows/apps/pr-triage-board"}
	for _, repo := range c.repos {
		args = append(args, "--repo "+repo)
	}
	args = append(args, fmt.Sprintf("--limit %d", c.limit))
	if c.maintainer != "" {
		args = append(args, "--maintainer "+c.maintainer)
	}
	return strings.Join(args, " ")
}

// dedupeStable keeps the first occurrence of every value, in order.
// slices.Compact would miss duplicates that are not adjacent.
func dedupeStable(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	deduped := make([]string, 0, len(values))
	for _, v := range values {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		deduped = append(deduped, v)
	}
	return deduped
}

// filterByMaintainer keeps the items where handle is a current owner or a
// CODEOWNERS owner of the changed files.
func filterByMaintainer(items []Item, handle string) []Item {
	handle = strings.TrimPrefix(handle, "@")
	has := func(list []string) bool {
		return slices.ContainsFunc(list, func(h string) bool { return strings.EqualFold(h, handle) })
	}
	var kept []Item
	for _, it := range items {
		if has(it.Current) || has(it.Owners) {
			kept = append(kept, it)
		}
	}
	return kept
}

// snapshotPattern matches the timestamp in the board header. The timestamp
// changes on every run and does not count as a change of the board.
var snapshotPattern = regexp.MustCompile("Auto-updated snapshot\\*\\*: `[^`]*`")

func sameBoard(a, b string) bool {
	normalize := func(s string) string {
		s = strings.ReplaceAll(s, "\r\n", "\n")
		return strings.TrimSpace(snapshotPattern.ReplaceAllString(s, "Auto-updated snapshot**: ``"))
	}
	return normalize(a) == normalize(b)
}

// updateIssue replaces the body of the issue unless the board did not change.
func updateIssue(ctx context.Context, api *client, repo string, number int, board string) error {
	if n := len([]rune(board)); n > maxIssueBodyChars {
		return fmt.Errorf("board has %d characters, above the %d characters GitHub allows in an issue body; lower --limit", n, maxIssueBodyChars)
	}
	current, err := api.issueBody(ctx, repo, number)
	if err != nil {
		return fmt.Errorf("read issue %s#%d: %w", repo, number, err)
	}
	if sameBoard(current, board) {
		logf("issue %s#%d is up to date, skipping the update", repo, number)
		return nil
	}
	if err := api.setIssueBody(ctx, repo, number, board); err != nil {
		return fmt.Errorf("update issue %s#%d: %w", repo, number, err)
	}
	logf("updated issue %s#%d", repo, number)
	return nil
}

type threadRecord struct {
	Resolved              bool      `json:"resolved"`
	LastAuthor            string    `json:"last_author"`
	LastAuthorAssociation string    `json:"last_author_association"`
	LastAt                time.Time `json:"last_at"`
	LastBody              string    `json:"last_body"`
	AuthorLastAt          time.Time `json:"author_last_at,omitzero"`
}

type reviewRecord struct {
	Author      string    `json:"author"`
	Association string    `json:"author_association"`
	State       string    `json:"state"`
	SubmittedAt time.Time `json:"submitted_at"`
}

// classificationRecord is the data of one pull request for a model that
// refines the deterministic bucket.
type classificationRecord struct {
	Repo           string         `json:"repo"`
	Number         int            `json:"number"`
	URL            string         `json:"url"`
	Title          string         `json:"title"`
	Author         string         `json:"author"`
	State          string         `json:"state"`
	Draft          bool           `json:"draft"`
	Mergeable      *bool          `json:"mergeable"`
	MergeableState string         `json:"mergeable_state"`
	Labels         []string       `json:"labels"`
	Additions      int            `json:"additions"`
	Deletions      int            `json:"deletions"`
	ChangedFiles   int            `json:"changed_files"`
	CI             CISummary      `json:"ci"`
	ReviewThreads  []threadRecord `json:"review_threads"`
	Reviews        []reviewRecord `json:"reviews"`
	Hygiene        []string       `json:"hygiene"`
	Bucket         Bucket         `json:"deterministic_bucket"`
	BucketReason   string         `json:"deterministic_reason"`
}

// classificationRequest converts items to the JSON input of a classification model.
func classificationRequest(items []Item) []classificationRecord {
	records := make([]classificationRecord, 0, len(items))
	for _, it := range items {
		pr := it.PR
		rec := classificationRecord{
			Repo:           pr.Repo,
			Number:         pr.Number,
			URL:            pr.URL,
			Title:          pr.Title,
			Author:         pr.Author,
			State:          "open",
			Draft:          pr.Draft,
			Mergeable:      pr.Mergeable,
			MergeableState: pr.MergeableState,
			Labels:         append([]string{}, pr.Labels...),
			Additions:      pr.Additions,
			Deletions:      pr.Deletions,
			ChangedFiles:   pr.ChangedFiles,
			CI:             it.Class.CI,
			ReviewThreads:  []threadRecord{},
			Reviews:        []reviewRecord{},
			Hygiene:        append([]string{}, it.Class.Hygiene...),
			Bucket:         it.Class.Bucket,
			BucketReason:   it.Class.Reason,
		}
		for _, t := range pr.Threads {
			rec.ReviewThreads = append(rec.ReviewThreads, threadRecord(t))
		}
		for _, r := range pr.Reviews {
			rec.Reviews = append(rec.Reviews, reviewRecord(r))
		}
		records = append(records, rec)
	}
	return records
}

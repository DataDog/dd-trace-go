// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var regexpBackticks = regexp.MustCompile("`[^`]*`")

var updateGolden = flag.Bool("update", false, "rewrite the golden files in testdata")

func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *updateGolden {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("board differs from %s, run `go test -run %s -update` to inspect the change\n--- got ---\n%s", path, t.Name(), got)
	}
}

// fixturePR returns a pull request with the given number, author and changes.
func fixturePR(number int, author string, mutate ...func(*PR)) *PR {
	return newTestPR(append([]func(*PR){func(p *PR) {
		p.Number = number
		p.Author = author
		p.URL = fmt.Sprintf("https://github.com/%s/pull/%d", p.Repo, number)
	}}, mutate...)...)
}

func fixtureItems(co *codeowners) []Item {
	reviewedByBob := func(p *PR) {
		p.Reviews = []Review{maintainerReview("bob", "COMMENTED", daysAgo(10))}
		p.RequestedReviewers = []string{"DataDog/dd-trace-go-guild"}
	}
	prs := []*PR{
		// Quick wins.
		fixturePR(101, "alice", func(p *PR) {
			p.Title = "fix(contrib/net/http): close body on error"
			p.Additions, p.Deletions, p.ChangedFiles = 20, 5, 2
			p.Files = []string{"contrib/net/http/http.go"}
			p.HeadCommitAt, p.UpdatedAt = daysAgo(3), daysAgo(3)
		}),
		fixturePR(102, "dave", func(p *PR) {
			p.Title = "docs: fix typo for @alice"
			p.Additions, p.Deletions, p.ChangedFiles = 1, 1, 1
			p.Files = []string{"README.md"}
			p.HeadCommitAt, p.UpdatedAt = daysAgo(8), daysAgo(8)
		}),

		// Updated after review.
		fixturePR(110, "erin", func(p *PR) {
			p.Title = "feat(appsec): add rule | new matcher"
			p.Files = []string{"appsec/waf.go", ".github/workflows/appsec.yml"}
			reviewedByBob(p)
			p.Reviews = append(p.Reviews, maintainerReview("carol", "APPROVED", daysAgo(9)))
			p.HeadCommitAt, p.UpdatedAt = daysAgo(2), daysAgo(2)
		}),

		// Blocked on workflow approval.
		fixturePR(120, "newbie", func(p *PR) {
			p.Title = "fix(profiler): typo in log message"
			p.AuthorAssociation = "FIRST_TIME_CONTRIBUTOR"
			p.Additions, p.Deletions, p.ChangedFiles = 2, 2, 1
			p.Files = []string{"profiler/profiler.go"}
			p.CheckRuns = nil
			p.ActionRequiredSuites = 1
			p.CreatedAt = daysAgo(12)
		}),
		fixturePR(121, "newbie2", func(p *PR) {
			p.Title = "ci: add workflow"
			p.AuthorAssociation = "FIRST_TIMER"
			p.Additions, p.Deletions, p.ChangedFiles = 40, 0, 1
			p.Files = []string{".github/workflows/new.yml"}
			p.CheckRuns = nil
			p.CreatedAt = daysAgo(2)
		}),

		// Waiting on maintainer.
		fixturePR(130, "frank", func(p *PR) {
			p.Title = "Refactor span pool"
			p.Files = []string{"ddtrace/tracer/span.go", "ddtrace/tracer/pool.go"}
			p.HeadCommitAt = daysAgo(40)
			p.CreatedAt = daysAgo(45)
			p.UpdatedAt = daysAgo(40)
		}),
		fixturePR(131, "grace", func(p *PR) {
			p.Title = "feat(ddtrace/tracer): add option"
			p.Files = []string{"ddtrace/tracer/option.go"}
			p.Reviews = []Review{maintainerReview("bob", "APPROVED", daysAgo(4))}
			p.Assignees = []string{"DataDog/apm-go"}
			p.HeadCommitAt = daysAgo(20)
			p.Threads = []Thread{{Resolved: true, LastAuthor: "bob", LastAuthorAssociation: "MEMBER", LastAt: daysAgo(4)}}
		}),

		// Waiting on author.
		fixturePR(140, "heidi", func(p *PR) {
			p.Title = "fix(ddtrace/tracer): race in flush"
			p.Reviews = []Review{maintainerReview("bob", "CHANGES_REQUESTED", daysAgo(6))}
			p.UpdatedAt = daysAgo(6)
		}),
		fixturePR(141, "ivan", func(p *PR) {
			p.Title = "fix(appsec): block list"
			p.Files = []string{"appsec/block.go"}
			p.Threads = []Thread{{LastAuthor: "carol", LastAuthorAssociation: "MEMBER", LastAt: daysAgo(1)}}
			p.UpdatedAt = daysAgo(1)
		}),

		// Failing CI.
		fixturePR(150, "judy", func(p *PR) {
			p.Title = "fix(profiler): leak"
			p.Files = []string{"profiler/profiler.go"}
			p.CheckRuns = []CheckRun{{Name: "unit-tests", Status: "completed", Conclusion: "failure"}}
			p.UpdatedAt = daysAgo(1)
		}),

		// Needs rebase.
		fixturePR(160, "mallory", func(p *PR) {
			p.Title = "chore: bump dependencies"
			p.Mergeable = new(false)
			p.MergeableState = "dirty"
			p.UpdatedAt = daysAgo(75)
		}),

		// Unclassified.
		fixturePR(170, "nick", func(p *PR) {
			p.Title = "test(ddtrace): more coverage"
			p.CheckRuns = []CheckRun{{Name: "unit-tests", Status: "in_progress"}}
			p.UpdatedAt = daysAgo(0)
		}),

		// Draft.
		fixturePR(180, "olivia", func(p *PR) {
			p.Title = "wip: new integration"
			p.Draft = true
			p.CreatedAt = daysAgo(9)
		}),
	}
	items := make([]Item, len(prs))
	for i, pr := range prs {
		items[i] = newItem(pr, co)
	}
	return items
}

const fixtureCodeowners = `
/.github/            @DataDog/dd-trace-go-guild
/appsec/             @DataDog/asm-go
/contrib/            @DataDog/apm-go @DataDog/apm-idm-go
/ddtrace/            @DataDog/apm-go
/profiler/           @DataDog/profiling-go
*.md                 @DataDog/dd-trace-go-guild
`

func TestRenderBoardGolden(t *testing.T) {
	co := parseCodeowners(fixtureCodeowners)
	items := fixtureItems(co)

	wantBuckets := map[int]Bucket{
		101: BucketQuickWin, 102: BucketQuickWin, 110: BucketUpdatedAfterReview,
		120: BucketBlockedWorkflowApproval, 121: BucketBlockedWorkflowApproval,
		130: BucketWaitingOnMaintainer, 131: BucketWaitingOnMaintainer,
		140: BucketWaitingOnAuthor, 141: BucketWaitingOnAuthor, 150: BucketFailingCI,
		160: BucketNeedsRebase, 170: BucketUnclassified, 180: BucketDraft,
	}
	for _, it := range items {
		if it.Class.Bucket != wantBuckets[it.PR.Number] {
			t.Errorf("fixture PR %d is in bucket %q, want %q", it.PR.Number, it.Class.Bucket, wantBuckets[it.PR.Number])
		}
	}

	got := renderBoard(items, boardOptions{
		Repos:     []string{"DataDog/dd-trace-go"},
		TotalOpen: map[string]int{"DataDog/dd-trace-go": 140},
		Analyzed:  len(items),
		Limit:     100,
		Now:       testNow,
		Command:   "go run ./.github/workflows/apps/pr-triage-board --repo DataDog/dd-trace-go --limit 100",
	})
	checkGolden(t, "board.golden.md", got)

	// Every handle must sit inside backticks so that an issue edit pings nobody.
	for line := range strings.SplitSeq(got, "\n") {
		stripped := regexpBackticks.ReplaceAllString(line, "")
		if strings.Contains(stripped, "@") && !strings.HasPrefix(stripped, "> ") {
			t.Errorf("unquoted mention in %q", line)
		}
	}
}

func TestRenderBoardMultiRepoAndMaintainer(t *testing.T) {
	co := parseCodeowners(fixtureCodeowners)
	a := newItem(fixturePR(1, "alice", func(p *PR) { p.Files = []string{"appsec/x.go"} }), co)
	b := newItem(fixturePR(2, "bob", func(p *PR) {
		p.Repo = "DataDog/other"
		p.URL = "https://github.com/DataDog/other/pull/2"
		p.Files = []string{"appsec/y.go"}
		p.Draft = true
	}), co)
	got := renderBoard([]Item{a, b}, boardOptions{
		Repos:      []string{"DataDog/dd-trace-go", "DataDog/other"},
		TotalOpen:  map[string]int{"DataDog/dd-trace-go": 10, "DataDog/other": 3},
		Analyzed:   5, // the maintainer filter dropped three of them
		Limit:      50,
		Now:        testNow,
		Command:    "go run ./.github/workflows/apps/pr-triage-board --repo DataDog/dd-trace-go --repo DataDog/other --limit 50 --maintainer @asm",
		Maintainer: "@DataDog/asm-go",
	})
	checkGolden(t, "board_multi_repo.golden.md", got)
}

func TestRenderBoardEmpty(t *testing.T) {
	got := renderBoard(nil, boardOptions{
		Repos:     []string{"DataDog/dd-trace-go"},
		TotalOpen: map[string]int{"DataDog/dd-trace-go": 0},
		Limit:     100,
		Now:       testNow,
		Command:   "cmd",
	})
	for _, want := range []string{"Analyzed `0`", "_No PRs currently in this bucket._", "Draft PRs (0)", "_None._"} {
		if !strings.Contains(got, want) {
			t.Errorf("empty board lacks %q", want)
		}
	}
}

func TestSameBoardIgnoresSnapshotTime(t *testing.T) {
	a := "> **Auto-updated snapshot**: `2026-10-27 13:00 UTC` · Analyzed `3`\r\nbody\r\n"
	b := "> **Auto-updated snapshot**: `2026-10-27 14:00 UTC` · Analyzed `3`\nbody"
	if !sameBoard(a, b) {
		t.Error("boards that differ only in the snapshot time must be the same")
	}
	if sameBoard(a, strings.Replace(b, "body", "other", 1)) {
		t.Error("boards with different content must differ")
	}
}

func TestTitleCellNeutralizesMarkdown(t *testing.T) {
	// Pull request titles are plain text on GitHub. Inside the issue body they
	// must render as literal text, never as links, emphasis, code or HTML.
	it := Item{PR: &PR{Title: "[link](https://evil.example) **bold** `code` <b>x</b> a|b \\| ~s~ _em_ @user"}}
	got := titleCell(it)
	for _, want := range []string{
		`\[link\]`,
		`\*\*bold\*\*`,
		"\\`code\\`",
		"&lt;b&gt;",
		`a\|b`,
		`\~s\~`,
		`\_em\_`,
		"`@user`",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("titleCell() = %s, lacks %s", got, want)
		}
	}
	for _, banned := range []string{"[link](", "**bold**", "<b>", "~s~", "_em_"} {
		if strings.Contains(got, banned) {
			t.Errorf("titleCell() = %s, still contains active markdown %s", got, banned)
		}
	}
}

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 10, 27, 13, 0, 0, 0, time.UTC)

// daysAgo returns the time n days before testNow.
func daysAgo(n int) time.Time { return testNow.AddDate(0, 0, -n) }

// newTestPR returns a pull request that waits on a maintainer: it is
// ready, has a valid title, passing CI, 120 changed lines and no review.
func newTestPR(mutate ...func(*PR)) *PR {
	pr := &PR{
		Repo:              "DataDog/dd-trace-go",
		Number:            1,
		Title:             "fix(ddtrace/tracer): handle nil span",
		URL:               "https://github.com/DataDog/dd-trace-go/pull/1",
		Author:            "alice",
		BaseRef:           "main",
		Mergeable:         new(true),
		MergeableState:    "clean",
		AuthorAssociation: "CONTRIBUTOR",
		CreatedAt:         daysAgo(30),
		UpdatedAt:         daysAgo(20),
		HeadCommitAt:      daysAgo(20),
		Additions:         100,
		Deletions:         20,
		ChangedFiles:      3,
		Files:             []string{"ddtrace/tracer/span.go"},
		CheckRuns:         []CheckRun{{Name: "unit-tests", Status: "completed", Conclusion: "success"}},
	}
	for _, m := range mutate {
		m(pr)
	}
	return pr
}

func maintainerReview(login, state string, at time.Time) Review {
	return Review{Author: login, Association: "MEMBER", State: state, SubmittedAt: at}
}

func TestClassify(t *testing.T) {
	failingRun := CheckRun{Name: "unit-tests", Status: "completed", Conclusion: "failure"}
	actionRequiredRun := CheckRun{Name: "unit-tests", Status: "completed", Conclusion: "action_required"}

	tests := []struct {
		name   string
		mutate func(*PR)
		want   Bucket
	}{
		// Rule 1: drafts.
		{"draft", func(p *PR) { p.Draft = true }, BucketDraft},
		{"draft beats needs rebase", func(p *PR) { p.Draft = true; p.Mergeable = new(false) }, BucketDraft},
		{"draft beats failing CI", func(p *PR) { p.Draft = true; p.CheckRuns = []CheckRun{failingRun} }, BucketDraft},

		// Rule 2: merge conflicts.
		{"mergeable false needs rebase", func(p *PR) { p.Mergeable = new(false) }, BucketNeedsRebase},
		{"dirty state needs rebase", func(p *PR) { p.MergeableState = "dirty" }, BucketNeedsRebase},
		{"unknown mergeability is not a conflict", func(p *PR) { p.Mergeable = nil; p.MergeableState = "unknown" }, BucketWaitingOnMaintainer},
		{"needs rebase beats workflow approval", func(p *PR) {
			p.Mergeable = new(false)
			p.AuthorAssociation = "FIRST_TIME_CONTRIBUTOR"
			p.CheckRuns = []CheckRun{actionRequiredRun}
		}, BucketNeedsRebase},
		{"needs rebase beats failing CI", func(p *PR) { p.Mergeable = new(false); p.CheckRuns = []CheckRun{failingRun} }, BucketNeedsRebase},

		// Rule 3: workflow approval.
		{"first-time contributor with action_required check run", func(p *PR) {
			p.AuthorAssociation = "FIRST_TIME_CONTRIBUTOR"
			p.CheckRuns = []CheckRun{actionRequiredRun}
		}, BucketBlockedWorkflowApproval},
		{"first-time contributor with action_required check suite", func(p *PR) {
			p.AuthorAssociation = "FIRST_TIMER"
			p.CheckRuns = nil
			p.ActionRequiredSuites = 1
		}, BucketBlockedWorkflowApproval},
		{"first-time contributor without any check", func(p *PR) {
			p.AuthorAssociation = "FIRST_TIME_CONTRIBUTOR"
			p.CheckRuns = nil
		}, BucketBlockedWorkflowApproval},
		{"workflow approval beats failing CI", func(p *PR) {
			p.AuthorAssociation = "FIRST_TIME_CONTRIBUTOR"
			p.CheckRuns = []CheckRun{actionRequiredRun, failingRun}
		}, BucketBlockedWorkflowApproval},
		{"first-time contributor with only a pending merge gate status is blocked", func(p *PR) {
			p.AuthorAssociation = "FIRST_TIME_CONTRIBUTOR"
			p.CheckRuns = nil
			p.Statuses = []Status{{Context: "devflow/mergegate", State: "pending"}}
		}, BucketBlockedWorkflowApproval},
		{"first-time contributor with a pending blocking status is unclassified", func(p *PR) {
			p.AuthorAssociation = "FIRST_TIME_CONTRIBUTOR"
			p.CheckRuns = nil
			p.Statuses = []Status{{Context: "ci/legacy", State: "pending"}}
		}, BucketUnclassified},
		{"first-time contributor with finished CI is not blocked", func(p *PR) {
			p.AuthorAssociation = "FIRST_TIME_CONTRIBUTOR"
		}, BucketWaitingOnMaintainer},
		{"action_required for a known contributor is unclassified", func(p *PR) {
			p.CheckRuns = []CheckRun{actionRequiredRun}
		}, BucketUnclassified},

		// Rule 4: failing CI.
		{"failing check", func(p *PR) { p.CheckRuns = []CheckRun{failingRun} }, BucketFailingCI},
		{"failing status", func(p *PR) { p.Statuses = []Status{{Context: "ci/legacy", State: "failure"}} }, BucketFailingCI},
		{"cancelled latest run fails", func(p *PR) {
			p.CheckRuns = []CheckRun{{Name: "unit-tests", Status: "completed", Conclusion: "cancelled"}}
		}, BucketFailingCI},
		{"failing codecov check is ignored", func(p *PR) {
			p.CheckRuns = append(p.CheckRuns, CheckRun{Name: "codecov/patch", Status: "completed", Conclusion: "failure"})
		}, BucketWaitingOnMaintainer},
		{"failing CI beats changes requested", func(p *PR) {
			p.CheckRuns = []CheckRun{failingRun}
			p.Reviews = []Review{maintainerReview("bob", "CHANGES_REQUESTED", daysAgo(5))}
		}, BucketFailingCI},

		// Rule 5: waiting on the author.
		{"changes requested beats waiting on maintainer", func(p *PR) {
			p.Reviews = []Review{maintainerReview("bob", "CHANGES_REQUESTED", daysAgo(5))}
		}, BucketWaitingOnAuthor},
		{"changes requested stays active after a comment review", func(p *PR) {
			p.Reviews = []Review{
				maintainerReview("bob", "CHANGES_REQUESTED", daysAgo(5)),
				maintainerReview("bob", "COMMENTED", daysAgo(4)),
			}
		}, BucketWaitingOnAuthor},
		{"approval replaces earlier changes requested", func(p *PR) {
			p.Reviews = []Review{
				maintainerReview("bob", "CHANGES_REQUESTED", daysAgo(5)),
				maintainerReview("bob", "APPROVED", daysAgo(4)),
			}
		}, BucketWaitingOnMaintainer},
		{"dismissal replaces earlier changes requested", func(p *PR) {
			p.Reviews = []Review{
				maintainerReview("bob", "CHANGES_REQUESTED", daysAgo(5)),
				maintainerReview("bob", "DISMISSED", daysAgo(4)),
			}
		}, BucketWaitingOnMaintainer},
		{"changes requested by someone without history in the repository is ignored", func(p *PR) {
			p.Reviews = []Review{{Author: "carol", Association: "NONE", State: "CHANGES_REQUESTED", SubmittedAt: daysAgo(5)}}
		}, BucketWaitingOnMaintainer},
		{"changes requested by the author is ignored", func(p *PR) {
			p.Reviews = []Review{maintainerReview("alice", "CHANGES_REQUESTED", daysAgo(5))}
		}, BucketWaitingOnMaintainer},
		{"unresolved thread ending with a maintainer comment", func(p *PR) {
			p.Threads = []Thread{{LastAuthor: "bob", LastAuthorAssociation: "MEMBER", LastAt: daysAgo(3)}}
		}, BucketWaitingOnAuthor},
		{"unresolved thread ending with a comment of a user without history", func(p *PR) {
			p.Threads = []Thread{{LastAuthor: "carol", LastAuthorAssociation: "NONE", LastAt: daysAgo(3)}}
		}, BucketWaitingOnMaintainer},
		{"unresolved thread ending with an author comment", func(p *PR) {
			p.Threads = []Thread{{LastAuthor: "alice", LastAt: daysAgo(3)}}
		}, BucketWaitingOnMaintainer},
		{"resolved thread ending with a reviewer comment", func(p *PR) {
			p.Threads = []Thread{{Resolved: true, LastAuthor: "bob", LastAuthorAssociation: "MEMBER", LastAt: daysAgo(3)}}
		}, BucketWaitingOnMaintainer},

		// Rule 6: waiting on a maintainer.
		{"waiting on maintainer", func(*PR) {}, BucketWaitingOnMaintainer},
		{"quick win", func(p *PR) { p.Additions, p.Deletions = 30, 10 }, BucketQuickWin},
		{"quick win at the 60 line limit", func(p *PR) { p.Additions, p.Deletions = 50, 10 }, BucketQuickWin},
		{"61 lines is not a quick win", func(p *PR) { p.Additions, p.Deletions = 50, 11 }, BucketWaitingOnMaintainer},
		{"small PR with a bad title is not a quick win", func(p *PR) {
			p.Additions, p.Deletions = 3, 1
			p.Title = "Fix the thing"
		}, BucketWaitingOnMaintainer},
		{"title rules apply to main only", func(p *PR) {
			p.Additions, p.Deletions = 3, 1
			p.Title = "Fix the thing"
			p.BaseRef = "release-v2.8"
		}, BucketQuickWin},
		{"small reviewed PR without an update is not a quick win", func(p *PR) {
			p.Additions, p.Deletions = 3, 1
			p.Reviews = []Review{maintainerReview("bob", "COMMENTED", daysAgo(5))}
		}, BucketWaitingOnMaintainer},
		{"author push after review beats quick win", func(p *PR) {
			p.Additions, p.Deletions = 3, 1
			p.Reviews = []Review{maintainerReview("bob", "COMMENTED", daysAgo(10))}
			p.HeadCommitAt = daysAgo(2)
		}, BucketUpdatedAfterReview},
		{"author comment after review", func(p *PR) {
			p.Reviews = []Review{maintainerReview("bob", "COMMENTED", daysAgo(10))}
			p.Comments = []Comment{{Author: "alice", CreatedAt: daysAgo(1)}}
		}, BucketUpdatedAfterReview},
		{"author thread reply after review", func(p *PR) {
			p.Reviews = []Review{maintainerReview("bob", "COMMENTED", daysAgo(10))}
			p.Threads = []Thread{{LastAuthor: "alice", LastAt: daysAgo(1)}}
		}, BucketUpdatedAfterReview},
		{"review after the last push is plain waiting", func(p *PR) {
			p.Reviews = []Review{maintainerReview("bob", "APPROVED", daysAgo(1))}
		}, BucketWaitingOnMaintainer},
		{"pending review does not count as a review", func(p *PR) {
			p.Additions, p.Deletions = 3, 1
			p.Reviews = []Review{maintainerReview("bob", "PENDING", daysAgo(10))}
			p.HeadCommitAt = daysAgo(2)
		}, BucketQuickWin},
		{"review by someone without history in the repository does not count as a review", func(p *PR) {
			p.Additions, p.Deletions = 3, 1
			p.Reviews = []Review{{Author: "carol", Association: "FIRST_TIME_CONTRIBUTOR", State: "COMMENTED", SubmittedAt: daysAgo(10)}}
			p.HeadCommitAt = daysAgo(2)
		}, BucketQuickWin},

		// Rule 7: fallback.
		{"running CI is unclassified", func(p *PR) {
			p.CheckRuns = []CheckRun{{Name: "unit-tests", Status: "in_progress"}}
		}, BucketUnclassified},
		{"no checks for a known contributor is unclassified", func(p *PR) { p.CheckRuns = nil }, BucketUnclassified},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classify(newTestPR(tc.mutate))
			if got.Bucket != tc.want {
				t.Errorf("bucket = %q (%s), want %q", got.Bucket, got.Reason, tc.want)
			}
			if got.Reason == "" {
				t.Error("reason is empty")
			}
		})
	}
}

func TestSummarizeCI(t *testing.T) {
	done := func(name, conclusion string) CheckRun {
		return CheckRun{Name: name, Status: "completed", Conclusion: conclusion}
	}
	tests := []struct {
		name         string
		runs         []CheckRun
		statuses     []Status
		wantState    string
		wantFailing  []string
		wantPending  []string
		wantCounting int
	}{
		{"no checks", nil, nil, ciPending, nil, nil, 0},
		{"all pass", []CheckRun{done("a", "success"), done("b", "skipped"), done("c", "neutral")}, []Status{{Context: "s", State: "success"}}, ciPass, nil, nil, 4},
		{"failure wins over pending", []CheckRun{done("a", "failure"), {Name: "b", Status: "queued"}}, nil, ciFail, []string{"a"}, []string{"b"}, 2},
		{"timed out and startup failure fail", []CheckRun{done("b", "timed_out"), done("a", "startup_failure")}, nil, ciFail, []string{"a", "b"}, nil, 2},
		{"duplicate names are reported once", []CheckRun{done("m", "failure"), done("m", "failure")}, nil, ciFail, []string{"m"}, nil, 2},
		{"cancelled fails", []CheckRun{done("a", "success"), done("b", "cancelled")}, nil, ciFail, []string{"b"}, nil, 2},
		{"stale fails", []CheckRun{done("a", "success"), done("b", "stale")}, nil, ciFail, []string{"b"}, nil, 2},
		{"completed without conclusion is pending", []CheckRun{done("a", "success"), done("b", "")}, nil, ciPending, nil, []string{"b"}, 2},
		{"action required is pending", []CheckRun{done("a", "success"), done("b", "action_required")}, nil, ciPending, nil, []string{"b"}, 2},
		{"devflow gate statuses are not blocking", []CheckRun{done("a", "success")}, []Status{{Context: "devflow/mergegate", State: "pending"}, {Context: "devflow/merge", State: "failure"}}, ciPass, nil, nil, 1},
		{"checks ignored by all-green are not blocking", []CheckRun{done("a", "success"), done("label_issues", "failure"), done("check-title", "failure")}, []Status{{Context: "dd-gitlab/default-pipeline", State: "failure"}, {Context: "DDCI Status", State: "pending"}}, ciPass, nil, nil, 1},
		{"pending status", nil, []Status{{Context: "s", State: "pending"}}, ciPending, nil, []string{"s"}, 1},
		{"error status fails", nil, []Status{{Context: "s", State: "error"}}, ciFail, []string{"s"}, nil, 1},
		{"codecov is not blocking", []CheckRun{done("a", "success"), done("Codecov Report", "failure")}, []Status{{Context: "codecov/project", State: "failure"}}, ciPass, nil, nil, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := summarizeCI(tc.runs, tc.statuses)
			if got.State != tc.wantState || got.Total != tc.wantCounting ||
				!slices.Equal(got.Failing, tc.wantFailing) || !slices.Equal(got.Pending, tc.wantPending) {
				t.Errorf("summarizeCI() = %+v, want state %s total %d failing %v pending %v",
					got, tc.wantState, tc.wantCounting, tc.wantFailing, tc.wantPending)
			}
		})
	}
}

func TestNonBlockingChecksIsConfigurable(t *testing.T) {
	old := NonBlockingChecks
	t.Cleanup(func() { NonBlockingChecks = old })
	NonBlockingChecks = []string{"flaky-suite"}
	runs := []CheckRun{{Name: "Flaky-Suite (linux)", Status: "completed", Conclusion: "failure"}}
	if got := summarizeCI(runs, nil); got.Total != 0 || len(got.Failing) != 0 {
		t.Errorf("summarizeCI() = %+v, want the check ignored", got)
	}
}

func TestHygieneTitle(t *testing.T) {
	tests := []struct {
		title string
		valid bool
	}{
		{"fix(ddtrace/tracer): handle nil span", true},
		{"feat: add option", true},
		{"revert(contrib/net/http.v2): undo change", true},
		{"ci(pr-triage-board): add generator", true},
		{"Fix: capitalized type", false},
		{"fix handle nil span", false},
		{"fix(Tracer): uppercase scope", false},
		{"fix(): empty scope", false},
		{"fix:no space", false},
		{"fix: ", false},
		{"wip: not an allowed type", false},
	}
	for _, tc := range tests {
		t.Run(tc.title, func(t *testing.T) {
			flags := hygiene(newTestPR(func(p *PR) { p.Title = tc.title }))
			if got := !slices.Contains(flags, hygieneTitleFormat); got != tc.valid {
				t.Errorf("title valid = %v, want %v", got, tc.valid)
			}
		})
	}
}

func TestHygieneMergeConflict(t *testing.T) {
	flags := hygiene(newTestPR(func(p *PR) { p.Mergeable = new(false) }))
	if !slices.Equal(flags, []string{hygieneMergeConflict}) {
		t.Errorf("hygiene() = %v, want [%s]", flags, hygieneMergeConflict)
	}
	if flags := hygiene(newTestPR()); len(flags) != 0 {
		t.Errorf("hygiene() = %v, want none", flags)
	}
}

func TestClassifyApproved(t *testing.T) {
	approvedReview := maintainerReview("bob", "APPROVED", daysAgo(3))
	tests := []struct {
		name    string
		reviews []Review
		want    bool
	}{
		{"no review", nil, false},
		{"maintainer approval", []Review{approvedReview}, true},
		{"approval and open change request from another maintainer", []Review{approvedReview, maintainerReview("dave", "CHANGES_REQUESTED", daysAgo(2))}, false},
		{"approval by a non-maintainer", []Review{{Author: "carol", Association: "NONE", State: "APPROVED", SubmittedAt: daysAgo(3)}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classify(newTestPR(func(p *PR) { p.Reviews = tc.reviews })).Approved
			if got != tc.want {
				t.Errorf("Approved = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestClassifyLastActor(t *testing.T) {
	pr := newTestPR(func(p *PR) {
		p.Reviews = []Review{maintainerReview("bob", "COMMENTED", daysAgo(10))}
		p.Comments = []Comment{{Author: "dave", CreatedAt: daysAgo(4)}}
		p.Threads = []Thread{{LastAuthor: "bob", LastAuthorAssociation: "MEMBER", LastAt: daysAgo(6)}}
	})
	got := classify(pr)
	if got.LastActor != "dave" || got.LastEvent != "comment" {
		t.Errorf("last actor = %s (%s), want dave (comment)", got.LastActor, got.LastEvent)
	}
}

func TestClassifyTouchesBuild(t *testing.T) {
	tests := []struct {
		file string
		want bool
	}{
		{".github/workflows/pr-triage-board.yml", true},
		{".github/actions/setup-go/action.yml", true},
		{".github/chainguard/self.pr-triage-board.sts.yaml", true},
		{".github/pull_request_template.md", false},
		{".github/CODEOWNERS", false},
		{"docker-compose.yml", true},
		{"docker-compose.yaml", true},
		{"scripts/lint.sh", true},
		{"Makefile", true},
		{".gitlab-ci.yml", true},
		{"contrib/gin-gonic/gin/go.mod", true},
		{"go.sum", true},
		{"v2/go.work", true},
		{"ddtrace/tracer/span.go", false},
		{"docs/Makefile.md", false},
	}
	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			got := classify(newTestPR(func(p *PR) { p.Files = []string{"README.md", tc.file} })).TouchesBuild
			if got != tc.want {
				t.Errorf("TouchesBuild = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWaitingSince(t *testing.T) {
	pr := newTestPR(func(p *PR) {
		p.CreatedAt = daysAgo(30)
		p.HeadCommitAt = daysAgo(50) // an old commit on a new branch
		p.Comments = []Comment{{Author: "alice", CreatedAt: daysAgo(7)}, {Author: "bob", CreatedAt: daysAgo(1)}}
	})
	if got, want := classify(pr).WaitingSince, daysAgo(7); !got.Equal(want) {
		t.Errorf("WaitingSince = %s, want %s", got, want)
	}
	pr.Comments = nil
	if got, want := classify(pr).WaitingSince, daysAgo(30); !got.Equal(want) {
		t.Errorf("WaitingSince = %s, want creation time %s", got, want)
	}
}

func TestWaitingSinceUsesAuthorReplyInThread(t *testing.T) {
	// A maintainer commented after the author replied, so the thread ends with
	// the maintainer. The reply of the author still counts.
	pr := newTestPR(func(p *PR) {
		p.CreatedAt = daysAgo(30)
		p.HeadCommitAt = daysAgo(20)
		p.Threads = []Thread{{
			Resolved:     true,
			LastAuthor:   "bob",
			LastAt:       daysAgo(1),
			AuthorLastAt: daysAgo(4),
		}}
	})
	if got, want := classify(pr).WaitingSince, daysAgo(4); !got.Equal(want) {
		t.Errorf("WaitingSince = %s, want the author reply %s", got, want)
	}
}

func TestTitlePatternMatchesValidationWorkflow(t *testing.T) {
	data, err := os.ReadFile("../../pull-request-title-validation.yml")
	if err != nil {
		t.Fatal(err)
	}
	var pinned string
	for line := range strings.SplitSeq(string(data), "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "REGEX="); ok {
			pinned = value
			break
		}
	}
	if pinned == "" {
		t.Fatal("pull-request-title-validation.yml has no REGEX= line")
	}
	// The workflow quotes the value for the shell. Only the quotes differ.
	if n := len(pinned); n >= 2 && (pinned[0] == '\'' || pinned[0] == '"') && pinned[n-1] == pinned[0] {
		pinned = pinned[1 : n-1]
	}
	if got := titlePattern.String(); got != pinned {
		t.Errorf("titlePattern = %s, want the workflow REGEX %s", got, pinned)
	}
}

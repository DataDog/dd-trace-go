// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Bucket is the board section a pull request belongs to.
type Bucket string

const (
	BucketDraft                   Bucket = "Draft"
	BucketNeedsRebase             Bucket = "Needs Rebase"
	BucketBlockedWorkflowApproval Bucket = "Blocked on Workflow Approval"
	BucketFailingCI               Bucket = "Failing CI"
	BucketWaitingOnAuthor         Bucket = "Waiting on Author"
	BucketQuickWin                Bucket = "Quick Wins"
	BucketUpdatedAfterReview      Bucket = "Updated After Review"
	BucketWaitingOnMaintainer     Bucket = "Waiting on Maintainer"
	BucketUnclassified            Bucket = "Unclassified"
)

// NonBlockingChecks lists case-insensitive substrings of check-run names and
// status contexts that the CI summary ignores. The list matches the
// ignored-name-patterns of .github/workflows/all-green.yml, plus codecov. The
// merge gate does not ignore codecov, but the board treats it as advisory. That
// entry is the one deliberate divergence. "devflow/merge" also covers
// devflow/mergegate, which stays pending until a pull request enters the merge
// queue.
var NonBlockingChecks = []string{"devflow/merge", "label_issues", "check-title", "dd-gitlab/", "DDCI Status", "codecov"}

const (
	// quickWinMaxLines is the largest diff (additions plus deletions) of a quick win.
	quickWinMaxLines = 60

	// titleRulesBaseRef is the only base branch for which
	// .github/workflows/pull-request-title-validation.yml enforces the title rules.
	titleRulesBaseRef = "main"
)

// titlePattern is the REGEX of .github/workflows/pull-request-title-validation.yml.
// Keep both in sync.
var titlePattern = regexp.MustCompile(`^(feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert)(\([a-z0-9/_\.-]+\))?: .+`)

// Hygiene flags.
const (
	hygieneTitleFormat   = "title-format"
	hygieneMergeConflict = "merge-conflict"
)

// CI states.
const (
	ciPass    = "pass"
	ciFail    = "fail"
	ciPending = "pending"
)

// PR is the data the classifier reads. The GitHub client fills it and drops
// bot-authored reviews, comments and review threads before classification.
type PR struct {
	Repo               string
	Number             int
	Title              string
	URL                string
	Author             string
	BaseRef            string
	Draft              bool
	Mergeable          *bool // nil when GitHub has not computed it yet
	MergeableState     string
	AuthorAssociation  string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	HeadCommitAt       time.Time
	Additions          int
	Deletions          int
	ChangedFiles       int
	Files              []string
	Labels             []string
	Assignees          []string
	RequestedReviewers []string
	Reviews            []Review
	Comments           []Comment
	Threads            []Thread
	CheckRuns          []CheckRun
	Statuses           []Status

	// ActionRequiredSuites counts check suites that wait for a maintainer to
	// approve the workflow run.
	ActionRequiredSuites int
}

// Review is a submitted pull request review.
type Review struct {
	Author      string
	Association string
	State       string
	SubmittedAt time.Time
}

// Comment is a top-level comment on the pull request conversation.
type Comment struct {
	Author    string
	CreatedAt time.Time
}

// Thread is a review thread. The Last fields describe its final comment.
type Thread struct {
	Resolved              bool
	LastAuthor            string
	LastAuthorAssociation string
	LastAt                time.Time
	LastBody              string

	// AuthorLastAt is the time of the latest comment of the pull request author
	// in the thread. It is zero when the author did not comment.
	AuthorLastAt time.Time
}

// CheckRun is a GitHub check run for the head commit.
type CheckRun struct {
	Name       string
	Status     string
	Conclusion string
	StartedAt  time.Time // zero when GitHub reports no start time
}

// Status is a commit status for the head commit.
type Status struct {
	Context string
	State   string
}

// CISummary is the outcome of all blocking checks of the head commit.
type CISummary struct {
	State   string   `json:"state"`
	Total   int      `json:"total"`
	Failing []string `json:"failing,omitempty"`
	Pending []string `json:"pending,omitempty"`
}

// Classification is the deterministic triage result for one pull request.
type Classification struct {
	Bucket           Bucket
	Reason           string
	CI               CISummary
	Hygiene          []string
	Approved         bool
	ChangesRequested bool
	TouchesBuild     bool
	LastActor        string
	LastEvent        string

	// WaitingSince is the time of the latest author activity. It is when the
	// pull request last became the maintainers' turn.
	WaitingSince time.Time
}

// Item is a classified pull request together with its ownership data.
type Item struct {
	PR       *PR
	Class    Classification
	Current  []string // people and teams on the pull request now
	Owners   []string // CODEOWNERS owners of the changed files
	Proposed []string // Owners, only when none of them is on the pull request
}

// newItem classifies pr and resolves its owners.
func newItem(pr *PR, co *codeowners) Item {
	owners := co.ownersOfFiles(pr.Files, pr.Author)
	current := currentOwners(pr)
	return Item{
		PR:       pr,
		Class:    classify(pr),
		Current:  current,
		Owners:   owners,
		Proposed: proposedOwners(current, owners),
	}
}

// classify places pr in the first matching bucket:
//
//  1. draft
//  2. merge conflict
//  3. first-time contributor with a pending workflow approval
//  4. failing CI
//  5. changes requested or an unanswered review thread
//  6. waiting on a maintainer, with the sub-cases quick win and updated after review
//  7. unclassified
func classify(pr *PR) Classification {
	c := Classification{
		CI:               summarizeCI(pr.CheckRuns, pr.Statuses),
		Hygiene:          hygiene(pr),
		ChangesRequested: changesRequested(pr),
		TouchesBuild:     slices.ContainsFunc(pr.Files, isBuildFile),
		WaitingSince:     latestAuthorActivity(pr),
	}
	c.Approved = !c.ChangesRequested && approved(pr)
	c.LastActor, c.LastEvent = lastActor(pr)

	switch {
	case pr.Draft:
		c.Bucket, c.Reason = BucketDraft, "pull request is a draft"
	case hasMergeConflict(pr):
		c.Bucket, c.Reason = BucketNeedsRebase, "pull request conflicts with its base branch"
	case isFirstTimeContributor(pr) && workflowApprovalPending(pr):
		c.Bucket, c.Reason = BucketBlockedWorkflowApproval, "first-time contributor and workflows wait for approval"
	case c.CI.State == ciFail:
		c.Bucket, c.Reason = BucketFailingCI, "blocking checks fail"
	case c.ChangesRequested:
		c.Bucket, c.Reason = BucketWaitingOnAuthor, "a maintainer requested changes"
	case hasUnansweredThread(pr):
		c.Bucket, c.Reason = BucketWaitingOnAuthor, "a review thread waits for the author"
	case c.CI.State == ciPass:
		c.Bucket, c.Reason = waitingOnMaintainerBucket(pr, c.Hygiene)
	default:
		c.Bucket, c.Reason = BucketUnclassified, "checks have not finished"
	}
	return c
}

// waitingOnMaintainerBucket picks the sub-case for a pull request that has
// green CI and no open feedback for the author.
func waitingOnMaintainerBucket(pr *PR, hygiene []string) (Bucket, string) {
	reviewedAt, reviewed := latestMaintainerReview(pr)
	switch {
	case reviewed && latestAuthorActivity(pr).After(reviewedAt):
		return BucketUpdatedAfterReview, "the author pushed or replied after the last maintainer review"
	case !reviewed && pr.Additions+pr.Deletions <= quickWinMaxLines && len(hygiene) == 0:
		return BucketQuickWin, "small diff, clean hygiene and no maintainer review yet"
	default:
		return BucketWaitingOnMaintainer, "feedback is resolved and checks pass"
	}
}

func hasMergeConflict(pr *PR) bool {
	return (pr.Mergeable != nil && !*pr.Mergeable) || pr.MergeableState == "dirty"
}

// isFirstTimeContributor reports whether GitHub asks a maintainer to approve
// the workflow runs of the author.
func isFirstTimeContributor(pr *PR) bool {
	switch pr.AuthorAssociation {
	case "FIRST_TIME_CONTRIBUTOR", "FIRST_TIMER", "NONE":
		return true
	}
	return false
}

// workflowApprovalPending reports whether the head commit has runs that wait
// for approval. A commit without any check run and without a blocking status
// also counts, because GitHub creates no check runs before the approval.
// External services such as the merge gate post statuses anyway, so
// non-blocking statuses do not count.
func workflowApprovalPending(pr *PR) bool {
	if pr.ActionRequiredSuites > 0 {
		return true
	}
	if slices.ContainsFunc(pr.CheckRuns, func(r CheckRun) bool {
		return strings.EqualFold(r.Conclusion, "action_required")
	}) {
		return true
	}
	return len(pr.CheckRuns) == 0 && !slices.ContainsFunc(pr.Statuses, func(s Status) bool {
		return !isNonBlocking(s.Context)
	})
}

// summarizeCI reduces check runs and statuses to one state. The state is fail
// when any blocking check fails, pass when all blocking checks succeed, and
// pending in every other case, including when no check exists.
func summarizeCI(runs []CheckRun, statuses []Status) CISummary {
	var s CISummary
	add := func(name, result string) {
		if isNonBlocking(name) {
			return
		}
		s.Total++
		switch result {
		case ciFail:
			s.Failing = append(s.Failing, name)
		case ciPending:
			s.Pending = append(s.Pending, name)
		}
	}
	for _, r := range runs {
		add(r.Name, checkRunResult(r))
	}
	for _, st := range statuses {
		add(st.Context, statusResult(st))
	}
	// Matrix jobs repeat the same name. Report each name once.
	s.Failing = slices.Compact(slices.Sorted(slices.Values(s.Failing)))
	s.Pending = slices.Compact(slices.Sorted(slices.Values(s.Pending)))
	switch {
	case len(s.Failing) > 0:
		s.State = ciFail
	case len(s.Pending) > 0 || s.Total == 0:
		s.State = ciPending
	default:
		s.State = ciPass
	}
	return s
}

func isNonBlocking(name string) bool {
	lower := strings.ToLower(name)
	return slices.ContainsFunc(NonBlockingChecks, func(sub string) bool {
		return strings.Contains(lower, strings.ToLower(sub))
	})
}

func checkRunResult(r CheckRun) string {
	if r.Status != "completed" {
		return ciPending
	}
	switch r.Conclusion {
	case "success", "neutral", "skipped":
		return ciPass
	case "action_required", "":
		return ciPending
	default:
		// The check-runs list returns the latest run per name, so a cancelled
		// or stale run has no newer run that replaces it.
		return ciFail
	}
}

func statusResult(s Status) string {
	switch s.State {
	case "success":
		return ciPass
	case "failure", "error":
		return ciFail
	default:
		return ciPending
	}
}

// hygiene returns the hygiene flags of pr. The repository enforces only the
// conventional-commit title and conflict-free merges.
func hygiene(pr *PR) []string {
	var flags []string
	if pr.BaseRef == titleRulesBaseRef && !titlePattern.MatchString(pr.Title) {
		flags = append(flags, hygieneTitleFormat)
	}
	if hasMergeConflict(pr) {
		flags = append(flags, hygieneMergeConflict)
	}
	return flags
}

// isBuildFile reports whether path configures CI or the build. Module
// manifests count at any depth because they change dependencies or the
// toolchain. The .github/chainguard/ policies count because they control CI
// tokens. Other files in .github/, such as issue templates, do not count.
func isBuildFile(file string) bool {
	switch path.Base(file) {
	case "go.mod", "go.sum", "go.work":
		return true
	}
	for _, prefix := range []string{".github/workflows/", ".github/actions/", ".github/chainguard/", ".gitlab/", "scripts/"} {
		if strings.HasPrefix(file, prefix) {
			return true
		}
	}
	switch file {
	case "Makefile", ".gitlab-ci.yml", "docker-compose.yaml", "docker-compose.yml":
		return true
	}
	return false
}

// isMaintainerAssociation reports whether a reviewer with this author
// association counts as a maintainer. GitHub reports organization members with
// a private membership as CONTRIBUTOR, so CONTRIBUTOR counts too. Only
// reviewers without any history in the repository do not count.
func isMaintainerAssociation(association string) bool {
	switch association {
	case "OWNER", "MEMBER", "COLLABORATOR", "CONTRIBUTOR":
		return true
	}
	return false
}

func isAuthor(pr *PR, login string) bool {
	return strings.EqualFold(login, pr.Author)
}

// maintainerReviews returns the submitted reviews of maintainers other than
// the author, oldest first.
func maintainerReviews(pr *PR) []Review {
	var reviews []Review
	for _, r := range pr.Reviews {
		if r.SubmittedAt.IsZero() || r.State == "PENDING" || isAuthor(pr, r.Author) || !isMaintainerAssociation(r.Association) {
			continue
		}
		reviews = append(reviews, r)
	}
	slices.SortStableFunc(reviews, func(a, b Review) int { return a.SubmittedAt.Compare(b.SubmittedAt) })
	return reviews
}

func latestMaintainerReview(pr *PR) (time.Time, bool) {
	reviews := maintainerReviews(pr)
	if len(reviews) == 0 {
		return time.Time{}, false
	}
	return reviews[len(reviews)-1].SubmittedAt, true
}

// activeReviewStates returns, for each maintainer, the state of their latest
// approval, change request or dismissal. A plain comment review keeps the
// earlier state, as on GitHub.
func activeReviewStates(pr *PR) map[string]string {
	states := make(map[string]string)
	for _, r := range maintainerReviews(pr) {
		switch r.State {
		case "APPROVED", "CHANGES_REQUESTED", "DISMISSED":
			states[strings.ToLower(r.Author)] = r.State
		}
	}
	return states
}

func changesRequested(pr *PR) bool {
	for _, state := range activeReviewStates(pr) {
		if state == "CHANGES_REQUESTED" {
			return true
		}
	}
	return false
}

func approved(pr *PR) bool {
	for _, state := range activeReviewStates(pr) {
		if state == "APPROVED" {
			return true
		}
	}
	return false
}

// hasUnansweredThread reports whether an unresolved review thread ends with a
// comment from a maintainer other than the author. Comments of users without
// history in the repository do not hold a pull request.
func hasUnansweredThread(pr *PR) bool {
	return slices.ContainsFunc(pr.Threads, func(t Thread) bool {
		return !t.Resolved && t.LastAuthor != "" && !isAuthor(pr, t.LastAuthor) &&
			isMaintainerAssociation(t.LastAuthorAssociation)
	})
}

// latestAuthorActivity returns the time of the author's last push, comment,
// review or thread reply. It never precedes the creation of the pull request.
func latestAuthorActivity(pr *PR) time.Time {
	latest := pr.CreatedAt
	later := func(t time.Time) {
		if t.After(latest) {
			latest = t
		}
	}
	later(pr.HeadCommitAt)
	for _, c := range pr.Comments {
		if isAuthor(pr, c.Author) {
			later(c.CreatedAt)
		}
	}
	for _, t := range pr.Threads {
		if isAuthor(pr, t.LastAuthor) {
			later(t.LastAt)
		}
		later(t.AuthorLastAt)
	}
	for _, r := range pr.Reviews {
		if isAuthor(pr, r.Author) {
			later(r.SubmittedAt)
		}
	}
	return latest
}

// lastActor returns the person behind the newest event of the pull request and
// the kind of that event.
func lastActor(pr *PR) (actor, event string) {
	var latest time.Time
	consider := func(at time.Time, who, kind string) {
		if who != "" && !at.Before(latest) {
			latest, actor, event = at, who, kind
		}
	}
	consider(pr.HeadCommitAt, pr.Author, "commit")
	for _, r := range pr.Reviews {
		if !r.SubmittedAt.IsZero() && r.State != "PENDING" {
			consider(r.SubmittedAt, r.Author, "review:"+r.State)
		}
	}
	for _, c := range pr.Comments {
		consider(c.CreatedAt, c.Author, "comment")
	}
	for _, t := range pr.Threads {
		consider(t.LastAt, t.LastAuthor, "review-thread")
	}
	return actor, event
}

// currentOwners returns the people and teams already involved: reviewers,
// requested reviewers and assignees, without the author.
func currentOwners(pr *PR) []string {
	var current []string
	add := func(login string) {
		if login == "" || isAuthor(pr, login) || slices.ContainsFunc(current, func(c string) bool { return strings.EqualFold(c, login) }) {
			return
		}
		current = append(current, login)
	}
	for _, r := range pr.Reviews {
		if !r.SubmittedAt.IsZero() && r.State != "PENDING" {
			add(r.Author)
		}
	}
	for _, login := range pr.RequestedReviewers {
		add(login)
	}
	for _, login := range pr.Assignees {
		add(login)
	}
	return current
}

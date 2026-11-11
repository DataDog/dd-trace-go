// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	apiBaseURL    = "https://api.github.com"
	apiVersion    = "2022-11-28"
	perPage       = 100
	maxAttempts   = 4
	gatherWorkers = 4

	// maxRateLimitWait is the longest pause the client accepts before it
	// retries a rate-limited request. A longer pause fails the run.
	maxRateLimitWait = 2 * time.Minute

	// maxBodyPreview limits the response text added to an error.
	maxBodyPreview = 300

	// maxThreadBodyChars limits the review comment text kept per thread.
	maxThreadBodyChars = 500
)

// client is a minimal GitHub REST and GraphQL client. It is safe for
// concurrent use.
type client struct {
	http    *http.Client
	baseURL string
	token   string
	sleep   func(ctx context.Context, d time.Duration) error
	now     func() time.Time
}

func newClient(token string) *client {
	return &client{
		http:    &http.Client{Timeout: 60 * time.Second},
		baseURL: apiBaseURL,
		token:   token,
		sleep:   sleepContext,
		now:     time.Now,
	}
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// do sends a request and returns the response body and headers. It retries
// network errors, server errors and rate limits with a backoff, and returns an
// error for everything else.
func (c *client) do(ctx context.Context, method, target string, body []byte) ([]byte, http.Header, error) {
	if !strings.HasPrefix(target, "http") {
		target = c.baseURL + target
	}
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, target, reader)
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", apiVersion)
		req.Header.Set("Authorization", "Bearer "+c.token)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		wait := time.Duration(1<<(attempt-1)) * time.Second
		resp, err := c.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			lastErr = fmt.Errorf("%s %s: %w", method, target, err)
		} else {
			data, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr != nil {
				lastErr = fmt.Errorf("%s %s: read body: %w", method, target, readErr)
			} else if resp.StatusCode < 300 {
				return data, resp.Header, nil
			} else {
				lastErr = fmt.Errorf("%s %s: %s: %s", method, target, resp.Status, preview(data))
				switch {
				case isRateLimited(resp):
					wait = c.rateLimitWait(resp.Header, wait)
					if wait > maxRateLimitWait {
						return nil, nil, fmt.Errorf("%w (rate limit resets in %s, above the %s limit)", lastErr, wait.Round(time.Second), maxRateLimitWait)
					}
				case resp.StatusCode == http.StatusNotFound:
					return nil, nil, &notFoundError{err: lastErr}
				case resp.StatusCode < 500:
					return nil, nil, lastErr
				}
			}
		}
		if attempt == maxAttempts {
			break
		}
		logf("retrying in %s after: %v", wait, lastErr)
		if err := c.sleep(ctx, wait); err != nil {
			return nil, nil, err
		}
	}
	return nil, nil, fmt.Errorf("giving up after %d attempts: %w", maxAttempts, lastErr)
}

func preview(data []byte) string {
	s := strings.TrimSpace(string(data))
	if len(s) > maxBodyPreview {
		s = s[:maxBodyPreview] + "..."
	}
	return s
}

// isRateLimited recognizes the primary and the secondary GitHub rate limits.
// A 403 without the rate limit headers is a permission error and does not
// retry.
func isRateLimited(resp *http.Response) bool {
	switch resp.StatusCode {
	case http.StatusTooManyRequests:
		return true
	case http.StatusForbidden:
		return resp.Header.Get("X-RateLimit-Remaining") == "0" ||
			resp.Header.Get("Retry-After") != ""
	}
	return false
}

// rateLimitWait returns how long to pause before a retry. It prefers the
// Retry-After header, then the reset time of the primary limit, then fallback.
func (c *client) rateLimitWait(h http.Header, fallback time.Duration) time.Duration {
	if s := h.Get("Retry-After"); s != "" {
		if secs, err := strconv.Atoi(s); err == nil {
			return time.Duration(secs+1) * time.Second
		}
	}
	if h.Get("X-RateLimit-Remaining") == "0" {
		if epoch, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			return max(time.Unix(epoch, 0).Sub(c.now()), 0) + time.Second
		}
	}
	return max(fallback, time.Minute)
}

func (c *client) getJSON(ctx context.Context, path string, out any) error {
	data, _, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("GET %s: decode response: %w", path, err)
	}
	return nil
}

// paginate walks every page of a list endpoint. page returns false to stop early.
func (c *client) paginate(ctx context.Context, path string, page func(data []byte) (more bool, err error)) error {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	next := path + sep + "per_page=" + strconv.Itoa(perPage)
	for next != "" {
		data, header, err := c.do(ctx, http.MethodGet, next, nil)
		if err != nil {
			return err
		}
		more, err := page(data)
		if err != nil {
			return fmt.Errorf("GET %s: decode response: %w", next, err)
		}
		if !more {
			return nil
		}
		next = nextPageLink(header.Get("Link"))
	}
	return nil
}

// nextPageLink extracts the rel="next" URL from a Link header.
func nextPageLink(header string) string {
	for part := range strings.SplitSeq(header, ",") {
		target, params, _ := strings.Cut(part, ";")
		if strings.Contains(params, `rel="next"`) {
			return strings.Trim(strings.TrimSpace(target), "<>")
		}
	}
	return ""
}

// listAll collects the items of every page. extract decodes one page.
func listAll[T any](ctx context.Context, c *client, path string, extract func([]byte) ([]T, error)) ([]T, error) {
	var all []T
	err := c.paginate(ctx, path, func(data []byte) (bool, error) {
		items, err := extract(data)
		all = append(all, items...)
		return true, err
	})
	return all, err
}

func decodeArray[T any](data []byte) ([]T, error) {
	var items []T
	err := json.Unmarshal(data, &items)
	return items, err
}

// decodeField returns a decoder for list endpoints that wrap the items in an object.
func decodeField[T any](field string) func([]byte) ([]T, error) {
	return func(data []byte) ([]T, error) {
		var wrapper map[string]json.RawMessage
		if err := json.Unmarshal(data, &wrapper); err != nil {
			return nil, err
		}
		var items []T
		if raw, ok := wrapper[field]; ok {
			if err := json.Unmarshal(raw, &items); err != nil {
				return nil, err
			}
		}
		return items, nil
	}
}

// transientGraphQLErrors lists lower-case fragments of the GraphQL error
// messages that GitHub returns with status 200 for temporary failures.
var transientGraphQLErrors = []string{
	"something went wrong while executing your query",
	"please try again",
	"timeout",
}

func isTransientGraphQLError(message string) bool {
	lower := strings.ToLower(message)
	return slices.ContainsFunc(transientGraphQLErrors, func(fragment string) bool {
		return strings.Contains(lower, fragment)
	})
}

// graphQL runs a query and decodes the data member of the response. A
// response with transient errors retries with the backoff of do. Other errors
// fail at once.
func (c *client) graphQL(ctx context.Context, query string, variables map[string]any, out any) error {
	payload, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return err
	}
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		data, _, err := c.do(ctx, http.MethodPost, "/graphql", payload)
		if err != nil {
			return err
		}
		var envelope struct {
			Data   json.RawMessage `json:"data"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		if err := json.Unmarshal(data, &envelope); err != nil {
			return fmt.Errorf("graphql: decode response: %w", err)
		}
		if len(envelope.Errors) == 0 {
			return json.Unmarshal(envelope.Data, out)
		}
		messages := make([]string, len(envelope.Errors))
		transient := true
		for i, e := range envelope.Errors {
			messages[i] = e.Message
			transient = transient && isTransientGraphQLError(e.Message)
		}
		lastErr = fmt.Errorf("graphql: %s", strings.Join(messages, "; "))
		if !transient {
			return lastErr
		}
		if attempt == maxAttempts {
			break
		}
		wait := time.Duration(1<<(attempt-1)) * time.Second
		logf("retrying in %s after: %v", wait, lastErr)
		if err := c.sleep(ctx, wait); err != nil {
			return err
		}
	}
	return fmt.Errorf("giving up after %d attempts: %w", maxAttempts, lastErr)
}

type apiUser struct {
	Login string `json:"login"`
	Type  string `json:"type"`
}

// isBot reports whether the account is an app or automation account.
func (u apiUser) isBot() bool {
	return u.Type == "Bot" || strings.HasSuffix(u.Login, "[bot]")
}

type apiPull struct {
	Number            int       `json:"number"`
	State             string    `json:"state"`
	Title             string    `json:"title"`
	HTMLURL           string    `json:"html_url"`
	Draft             bool      `json:"draft"`
	User              apiUser   `json:"user"`
	AuthorAssociation string    `json:"author_association"`
	Mergeable         *bool     `json:"mergeable"`
	MergeableState    string    `json:"mergeable_state"`
	Additions         int       `json:"additions"`
	Deletions         int       `json:"deletions"`
	ChangedFiles      int       `json:"changed_files"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	Head              struct {
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	Assignees          []apiUser `json:"assignees"`
	RequestedReviewers []apiUser `json:"requested_reviewers"`
	RequestedTeams     []struct {
		Slug string `json:"slug"`
	} `json:"requested_teams"`
}

type apiFile struct {
	Filename string `json:"filename"`
}

type apiReview struct {
	User              apiUser   `json:"user"`
	State             string    `json:"state"`
	AuthorAssociation string    `json:"author_association"`
	SubmittedAt       time.Time `json:"submitted_at"`
}

type apiComment struct {
	User      apiUser   `json:"user"`
	CreatedAt time.Time `json:"created_at"`
}

type apiCheckRun struct {
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	Conclusion string    `json:"conclusion"`
	StartedAt  time.Time `json:"started_at"`
}

type apiStatus struct {
	Context string `json:"context"`
	State   string `json:"state"`
}

type apiCheckSuite struct {
	Conclusion string `json:"conclusion"`
}

// openPullNumbers returns the numbers of the limit most recently updated open
// pull requests.
func (c *client) openPullNumbers(ctx context.Context, repo string, limit int) ([]int, error) {
	var numbers []int
	path := fmt.Sprintf("/repos/%s/pulls?state=open&sort=updated&direction=desc", repo)
	err := c.paginate(ctx, path, func(data []byte) (bool, error) {
		pulls, err := decodeArray[struct {
			Number int `json:"number"`
		}](data)
		for _, p := range pulls {
			numbers = append(numbers, p.Number)
		}
		return len(numbers) < limit, err
	})
	if len(numbers) > limit {
		numbers = numbers[:limit]
	}
	return numbers, err
}

// openPullCount returns the number of all open pull requests of repo.
func (c *client) openPullCount(ctx context.Context, repo string) (int, error) {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return 0, err
	}
	var out struct {
		Repository struct {
			PullRequests struct {
				TotalCount int `json:"totalCount"`
			} `json:"pullRequests"`
		} `json:"repository"`
	}
	const query = `query($owner: String!, $name: String!) {
  repository(owner: $owner, name: $name) { pullRequests(states: OPEN) { totalCount } }
}`
	err = c.graphQL(ctx, query, map[string]any{"owner": owner, "name": name}, &out)
	return out.Repository.PullRequests.TotalCount, err
}

func splitRepo(repo string) (owner, name string, err error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", "", fmt.Errorf("invalid repository %q, want owner/name", repo)
	}
	return owner, name, nil
}

// gatherRepo fetches the limit most recently updated open pull requests with
// everything the classifier needs. It skips bot-authored and closed pull
// requests. It fails on the first failed request and cancels the other
// requests, so the board never shows partial data and a persistent failure
// does not use the quota until the job times out.
func (c *client) gatherRepo(ctx context.Context, repo string, limit int) ([]*PR, error) {
	numbers, err := c.openPullNumbers(ctx, repo, limit)
	if err != nil {
		return nil, fmt.Errorf("list open pull requests of %s: %w", repo, err)
	}
	prs := make([]*PR, len(numbers))
	gatherCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	sem := make(chan struct{}, gatherWorkers)
	var wg sync.WaitGroup
	for i, number := range numbers {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			if gatherCtx.Err() != nil {
				return
			}
			pr, err := c.gatherPR(gatherCtx, repo, number)
			if err != nil {
				// Only the first cause counts. Later errors are mostly the
				// cancellation itself.
				cancel(fmt.Errorf("%s#%d: %w", repo, number, err))
				return
			}
			prs[i] = pr
		})
	}
	wg.Wait()
	if err := context.Cause(gatherCtx); err != nil {
		return nil, err
	}
	kept := prs[:0]
	for _, pr := range prs {
		if pr != nil {
			kept = append(kept, pr)
		}
	}
	return kept, nil
}

// gatherPR fetches one pull request. It returns nil for bot-authored and
// closed pull requests.
func (c *client) gatherPR(ctx context.Context, repo string, number int) (*PR, error) {
	pullPath := fmt.Sprintf("/repos/%s/pulls/%d", repo, number)
	var pull apiPull
	if err := c.getJSON(ctx, pullPath, &pull); err != nil {
		return nil, err
	}
	if pull.User.isBot() || pull.State != "open" {
		return nil, nil
	}
	if pull.Mergeable == nil {
		// GitHub computes mergeability lazily. Ask once more.
		if err := c.sleep(ctx, 2*time.Second); err != nil {
			return nil, err
		}
		if err := c.getJSON(ctx, pullPath, &pull); err != nil {
			return nil, err
		}
	}

	pr := &PR{
		Repo:              repo,
		Number:            pull.Number,
		Title:             pull.Title,
		URL:               pull.HTMLURL,
		Author:            pull.User.Login,
		BaseRef:           pull.Base.Ref,
		Draft:             pull.Draft,
		Mergeable:         pull.Mergeable,
		MergeableState:    pull.MergeableState,
		AuthorAssociation: pull.AuthorAssociation,
		CreatedAt:         pull.CreatedAt,
		UpdatedAt:         pull.UpdatedAt,
		Additions:         pull.Additions,
		Deletions:         pull.Deletions,
		ChangedFiles:      pull.ChangedFiles,
	}
	for _, l := range pull.Labels {
		pr.Labels = append(pr.Labels, l.Name)
	}
	for _, u := range pull.Assignees {
		pr.Assignees = append(pr.Assignees, u.Login)
	}
	owner, _, _ := splitRepo(repo)
	for _, u := range pull.RequestedReviewers {
		pr.RequestedReviewers = append(pr.RequestedReviewers, u.Login)
	}
	for _, t := range pull.RequestedTeams {
		pr.RequestedReviewers = append(pr.RequestedReviewers, owner+"/"+t.Slug)
	}

	sha := pull.Head.SHA
	files, err := listAll(ctx, c, pullPath+"/files", decodeArray[apiFile])
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		pr.Files = append(pr.Files, f.Filename)
	}

	reviews, err := listAll(ctx, c, pullPath+"/reviews", decodeArray[apiReview])
	if err != nil {
		return nil, err
	}
	for _, r := range reviews {
		if r.User.isBot() {
			continue
		}
		pr.Reviews = append(pr.Reviews, Review{Author: r.User.Login, Association: r.AuthorAssociation, State: r.State, SubmittedAt: r.SubmittedAt})
	}

	comments, err := listAll(ctx, c, fmt.Sprintf("/repos/%s/issues/%d/comments", repo, number), decodeArray[apiComment])
	if err != nil {
		return nil, err
	}
	for _, cm := range comments {
		if cm.User.isBot() {
			continue
		}
		pr.Comments = append(pr.Comments, Comment{Author: cm.User.Login, CreatedAt: cm.CreatedAt})
	}

	runs, err := listAll(ctx, c, fmt.Sprintf("/repos/%s/commits/%s/check-runs", repo, sha), decodeField[apiCheckRun]("check_runs"))
	if err != nil {
		return nil, err
	}
	for _, r := range runs {
		pr.CheckRuns = append(pr.CheckRuns, CheckRun(r))
	}
	statuses, err := listAll(ctx, c, fmt.Sprintf("/repos/%s/commits/%s/status", repo, sha), decodeField[apiStatus]("statuses"))
	if err != nil {
		return nil, err
	}
	for _, s := range statuses {
		pr.Statuses = append(pr.Statuses, Status{Context: s.Context, State: s.State})
	}
	suites, err := listAll(ctx, c, fmt.Sprintf("/repos/%s/commits/%s/check-suites", repo, sha), decodeField[apiCheckSuite]("check_suites"))
	if err != nil {
		return nil, err
	}
	for _, s := range suites {
		if s.Conclusion == "action_required" {
			pr.ActionRequiredSuites++
		}
	}

	var pushedAt, committedAt time.Time
	pr.Threads, pushedAt, committedAt, err = c.reviewThreadsAndHead(ctx, repo, number, pull.User.Login)
	if err != nil {
		return nil, err
	}
	pr.HeadCommitAt = headTime(pushedAt, committedAt, pr.CheckRuns)
	return pr, nil
}

// headTime returns the time the head commit reached the pull request. The
// push time is exact. GraphQL leaves it empty for some commits. The first CI
// run of the head commit then approximates the push. The commit time is the
// last resort, because a commit can predate its push by far, for example after
// a rebase.
func headTime(pushedAt, committedAt time.Time, runs []CheckRun) time.Time {
	if !pushedAt.IsZero() {
		return pushedAt
	}
	var first time.Time
	for _, r := range runs {
		if !r.StartedAt.IsZero() && (first.IsZero() || r.StartedAt.Before(first)) {
			first = r.StartedAt
		}
	}
	if !first.IsZero() {
		return first
	}
	return committedAt
}

const reviewThreadsQuery = `query($owner: String!, $name: String!, $number: Int!, $cursor: String) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      commits(last: 1) { nodes { commit { committedDate pushedDate } } }
      reviewThreads(first: 100, after: $cursor) {
        pageInfo { hasNextPage endCursor }
        nodes {
          isResolved
          comments(last: 100) { nodes { author { login __typename } authorAssociation body createdAt } }
        }
      }
    }
  }
}`

// reviewThreadsAndHead fetches the review threads of a pull request and the
// push and commit times of its head commit. The push time is zero when GitHub
// reports none. REST has no endpoint for review threads. The push time comes
// from GraphQL because a commit object can predate its push, for example after
// a force push. Threads that end with a bot comment are left out.
//
// Each thread reads its last 100 comments. A thread with more comments loses
// the author replies before them, which is acceptable for a wait estimate.
// author is the login of the pull request author, for Thread.AuthorLastAt.
func (c *client) reviewThreadsAndHead(ctx context.Context, repo string, number int, author string) (threads []Thread, pushedAt, committedAt time.Time, err error) {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return nil, time.Time{}, time.Time{}, err
	}
	var cursor *string
	for {
		var out struct {
			Repository struct {
				PullRequest struct {
					Commits struct {
						Nodes []struct {
							Commit struct {
								CommittedDate time.Time  `json:"committedDate"`
								PushedDate    *time.Time `json:"pushedDate"`
							} `json:"commit"`
						} `json:"nodes"`
					} `json:"commits"`
					ReviewThreads struct {
						PageInfo struct {
							HasNextPage bool   `json:"hasNextPage"`
							EndCursor   string `json:"endCursor"`
						} `json:"pageInfo"`
						Nodes []struct {
							IsResolved bool `json:"isResolved"`
							Comments   struct {
								Nodes []struct {
									Author *struct {
										Login    string `json:"login"`
										Typename string `json:"__typename"`
									} `json:"author"`
									AuthorAssociation string    `json:"authorAssociation"`
									Body              string    `json:"body"`
									CreatedAt         time.Time `json:"createdAt"`
								} `json:"nodes"`
							} `json:"comments"`
						} `json:"nodes"`
					} `json:"reviewThreads"`
				} `json:"pullRequest"`
			} `json:"repository"`
		}
		vars := map[string]any{"owner": owner, "name": name, "number": number, "cursor": cursor}
		if err := c.graphQL(ctx, reviewThreadsQuery, vars, &out); err != nil {
			return nil, time.Time{}, time.Time{}, err
		}
		if committedAt.IsZero() {
			if nodes := out.Repository.PullRequest.Commits.Nodes; len(nodes) > 0 {
				committedAt = nodes[0].Commit.CommittedDate
				if nodes[0].Commit.PushedDate != nil {
					pushedAt = *nodes[0].Commit.PushedDate
				}
			}
		}
		page := out.Repository.PullRequest.ReviewThreads
		for _, n := range page.Nodes {
			if len(n.Comments.Nodes) == 0 {
				continue
			}
			last := n.Comments.Nodes[len(n.Comments.Nodes)-1]
			lastAuthor := "ghost"
			if last.Author != nil {
				if last.Author.Typename == "Bot" || strings.HasSuffix(last.Author.Login, "[bot]") {
					continue
				}
				lastAuthor = last.Author.Login
			}
			var authorLastAt time.Time
			for _, cm := range n.Comments.Nodes {
				if cm.Author != nil && strings.EqualFold(cm.Author.Login, author) && cm.CreatedAt.After(authorLastAt) {
					authorLastAt = cm.CreatedAt
				}
			}
			threads = append(threads, Thread{
				Resolved:              n.IsResolved,
				LastAuthor:            lastAuthor,
				LastAuthorAssociation: last.AuthorAssociation,
				LastAt:                last.CreatedAt,
				LastBody:              truncate(last.Body, maxThreadBodyChars),
				AuthorLastAt:          authorLastAt,
			})
		}
		if !page.PageInfo.HasNextPage {
			return threads, pushedAt, committedAt, nil
		}
		cursor = &page.PageInfo.EndCursor
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

// fetchCodeowners reads the CODEOWNERS file of repo at ref through the
// contents API. GitHub evaluates ownership on the base branch of a pull
// request, so ref is that branch. GitHub looks for the file in this order. A
// repository without the file has no owners.
func (c *client) fetchCodeowners(ctx context.Context, repo, ref string) (*codeowners, error) {
	for _, path := range []string{".github/CODEOWNERS", "CODEOWNERS", "docs/CODEOWNERS"} {
		var file struct {
			Encoding string `json:"encoding"`
			Content  string `json:"content"`
		}
		err := c.getJSON(ctx, fmt.Sprintf("/repos/%s/contents/%s?ref=%s", repo, path, url.QueryEscape(ref)), &file)
		if _, ok := errors.AsType[*notFoundError](err); ok {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("fetch %s of %s: %w", path, repo, err)
		}
		if file.Encoding != "base64" {
			return nil, fmt.Errorf("fetch %s of %s: unsupported encoding %q", path, repo, file.Encoding)
		}
		text, err := base64.StdEncoding.DecodeString(strings.NewReplacer("\n", "", "\r", "").Replace(file.Content))
		if err != nil {
			return nil, fmt.Errorf("decode %s of %s: %w", path, repo, err)
		}
		return parseCodeowners(string(text)), nil
	}
	logf("%s has no CODEOWNERS file on %s, owners are not proposed", repo, ref)
	return parseCodeowners(""), nil
}

// notFoundError marks a 404 response.
type notFoundError struct{ err error }

func (e *notFoundError) Error() string { return e.err.Error() }
func (e *notFoundError) Unwrap() error { return e.err }

// issueBody returns the body of an issue.
func (c *client) issueBody(ctx context.Context, repo string, number int) (string, error) {
	var issue struct {
		Body string `json:"body"`
	}
	if err := c.getJSON(ctx, fmt.Sprintf("/repos/%s/issues/%d", repo, number), &issue); err != nil {
		return "", err
	}
	return issue.Body, nil
}

// setIssueBody replaces the body of an issue.
func (c *client) setIssueBody(ctx context.Context, repo string, number int, body string) error {
	payload, err := json.Marshal(map[string]string{"body": body})
	if err != nil {
		return err
	}
	_, _, err = c.do(ctx, http.MethodPatch, fmt.Sprintf("/repos/%s/issues/%d", repo, number), payload)
	return err
}

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
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	logOutput = io.Discard
	os.Exit(m.Run())
}

// testClient returns a client for srv that never waits and records the waits.
func testClient(srv *httptest.Server) (*client, *[]time.Duration) {
	var mu sync.Mutex
	var waits []time.Duration
	c := newClient("test-token")
	c.baseURL = srv.URL
	c.sleep = func(_ context.Context, d time.Duration) error {
		mu.Lock()
		defer mu.Unlock()
		waits = append(waits, d)
		return nil
	}
	return c, &waits
}

func TestClientSendsGitHubHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for header, want := range map[string]string{
			"Accept":               "application/vnd.github+json",
			"X-GitHub-Api-Version": "2022-11-28",
			"Authorization":        "Bearer test-token",
		} {
			if got := r.Header.Get(header); got != want {
				t.Errorf("%s = %q, want %q", header, got, want)
			}
		}
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()
	c, _ := testClient(srv)
	if err := c.getJSON(t.Context(), "/x", &struct{}{}); err != nil {
		t.Fatal(err)
	}
}

func TestClientRetries(t *testing.T) {
	tests := []struct {
		name      string
		respond   func(w http.ResponseWriter, call int)
		wantErr   string
		wantCalls int
		wantWaits []time.Duration
		now       time.Time // replaces the clock of the client when set
	}{
		{
			name: "server error then success",
			respond: func(w http.ResponseWriter, call int) {
				if call == 1 {
					http.Error(w, "bad gateway", http.StatusBadGateway)
					return
				}
				fmt.Fprint(w, `{}`)
			},
			wantCalls: 2,
			wantWaits: []time.Duration{time.Second},
		},
		{
			name: "secondary rate limit honors Retry-After",
			respond: func(w http.ResponseWriter, call int) {
				if call == 1 {
					w.Header().Set("Retry-After", "7")
					http.Error(w, "You have exceeded a secondary rate limit", http.StatusForbidden)
					return
				}
				fmt.Fprint(w, `{}`)
			},
			wantCalls: 2,
			wantWaits: []time.Duration{8 * time.Second},
		},
		{
			name: "plain permission error fails fast",
			respond: func(w http.ResponseWriter, _ int) {
				http.Error(w, "Resource not accessible by integration. API rate limit is not the problem.", http.StatusForbidden)
			},
			wantErr:   "403",
			wantCalls: 1,
		},
		{
			name: "too many requests retries after the fallback wait",
			respond: func(w http.ResponseWriter, call int) {
				if call == 1 {
					http.Error(w, "slow down", http.StatusTooManyRequests)
					return
				}
				fmt.Fprint(w, `{}`)
			},
			wantCalls: 2,
			wantWaits: []time.Duration{time.Minute},
		},
		{
			name: "primary rate limit waits until the reset on the injected clock",
			respond: func(w http.ResponseWriter, call int) {
				if call == 1 {
					w.Header().Set("X-RateLimit-Remaining", "0")
					w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(testNow.Add(30*time.Second).Unix(), 10))
					http.Error(w, "API rate limit exceeded", http.StatusForbidden)
					return
				}
				fmt.Fprint(w, `{}`)
			},
			wantCalls: 2,
			wantWaits: []time.Duration{31 * time.Second},
			now:       testNow,
		},
		{
			name: "rate limit that resets too late fails",
			respond: func(w http.ResponseWriter, _ int) {
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
				http.Error(w, "API rate limit exceeded", http.StatusForbidden)
			},
			wantErr:   "rate limit resets in",
			wantCalls: 1,
		},
		{
			name:      "client error is not retried",
			respond:   func(w http.ResponseWriter, _ int) { http.Error(w, "nope", http.StatusUnprocessableEntity) },
			wantErr:   "422",
			wantCalls: 1,
		},
		{
			name:      "persistent server error gives up",
			respond:   func(w http.ResponseWriter, _ int) { http.Error(w, "boom", http.StatusInternalServerError) },
			wantErr:   "giving up after 4 attempts",
			wantCalls: 4,
			wantWaits: []time.Duration{time.Second, 2 * time.Second, 4 * time.Second},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				tc.respond(w, calls)
			}))
			defer srv.Close()
			c, waits := testClient(srv)
			if !tc.now.IsZero() {
				c.now = func() time.Time { return tc.now }
			}
			err := c.getJSON(t.Context(), "/x", &struct{}{})
			if tc.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
			}
			if calls != tc.wantCalls {
				t.Errorf("calls = %d, want %d", calls, tc.wantCalls)
			}
			if fmt.Sprint(*waits) != fmt.Sprint(tc.wantWaits) {
				t.Errorf("waits = %v, want %v", *waits, tc.wantWaits)
			}
		})
	}
}

func TestClientPagination(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("per_page"); got != "100" {
			t.Errorf("per_page = %q, want 100", got)
		}
		switch r.URL.Query().Get("page") {
		case "":
			w.Header().Set("Link", fmt.Sprintf(`<%s/items?per_page=100&page=2>; rel="next", <%s/items?page=3>; rel="last"`, srv.URL, srv.URL))
			fmt.Fprint(w, `[{"filename":"a"},{"filename":"b"}]`)
		case "2":
			fmt.Fprint(w, `[{"filename":"c"}]`)
		}
	}))
	defer srv.Close()
	c, _ := testClient(srv)
	files, err := listAll(t.Context(), c, "/items", decodeArray[apiFile])
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 || files[2].Filename != "c" {
		t.Errorf("files = %v, want a, b, c", files)
	}
}

func TestOpenPullNumbersHonorsLimit(t *testing.T) {
	requests := 0
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		q := r.URL.Query()
		if q.Get("state") != "open" || q.Get("sort") != "updated" || q.Get("direction") != "desc" {
			t.Errorf("unexpected query %v", q)
		}
		w.Header().Set("Link", fmt.Sprintf(`<%s/repos/o/r/pulls?page=2>; rel="next"`, srv.URL))
		fmt.Fprint(w, `[{"number":9},{"number":8},{"number":7}]`)
	}))
	defer srv.Close()
	c, _ := testClient(srv)
	numbers, err := c.openPullNumbers(t.Context(), "o/r", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(numbers) != 2 || numbers[0] != 9 || numbers[1] != 8 || requests != 1 {
		t.Errorf("numbers = %v after %d requests, want [9 8] after 1", numbers, requests)
	}
}

func TestNextPageLink(t *testing.T) {
	tests := map[string]string{
		"":                                 "",
		`<https://x/a?page=2>; rel="next"`: "https://x/a?page=2",
		`<https://x/a?page=1>; rel="prev", <https://x/a?page=3>; rel="next"`: "https://x/a?page=3",
		`<https://x/a?page=9>; rel="last"`:                                   "",
	}
	for header, want := range tests {
		if got := nextPageLink(header); got != want {
			t.Errorf("nextPageLink(%q) = %q, want %q", header, got, want)
		}
	}
}

func TestFetchCodeownersLookupOrder(t *testing.T) {
	// GitHub uses .github/CODEOWNERS first, then the root file, then docs/.
	tests := []struct {
		name    string
		missing []string
		want    string
	}{
		{name: "github dir wins", missing: nil, want: "DataDog/asm-go"},
		{name: "root file wins over docs", missing: []string{".github"}, want: "DataDog/root"},
		{name: "docs file used last", missing: []string{".github", ""}, want: "DataDog/docs"},
	}
	texts := map[string]string{
		"":        "/appsec/ @DataDog/root\n",
		".github": "/appsec/ @DataDog/asm-go\n",
		"docs":    "/appsec/ @DataDog/docs\n",
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			missing := map[string]bool{}
			for _, m := range tc.missing {
				missing[m] = true
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.URL.Query().Get("ref"); got != "release/v2" {
					t.Errorf("ref = %q, want release/v2", got)
				}
				if got := r.URL.RawQuery; got != "ref=release%2Fv2" {
					t.Errorf("raw query = %q, want an escaped ref", got)
				}
				locs := map[string]string{
					"/repos/o/r/contents/.github/CODEOWNERS": ".github",
					"/repos/o/r/contents/CODEOWNERS":         "",
					"/repos/o/r/contents/docs/CODEOWNERS":    "docs",
				}
				loc, ok := locs[r.URL.Path]
				if !ok {
					t.Errorf("unexpected path %s", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				if missing[loc] {
					http.NotFound(w, r)
					return
				}
				// GitHub wraps base64 content at 60 characters.
				enc := base64.StdEncoding.EncodeToString([]byte(texts[loc]))
				json.NewEncoder(w).Encode(map[string]string{"encoding": "base64", "content": enc[:8] + "\n" + enc[8:] + "\n"})
			}))
			defer srv.Close()
			c, _ := testClient(srv)
			co, err := c.fetchCodeowners(t.Context(), "o/r", "release/v2")
			if err != nil {
				t.Fatal(err)
			}
			if got := co.ownersOf("appsec/x.go"); len(got) != 1 || got[0] != tc.want {
				t.Errorf("ownersOf() = %v, want [%s]", got, tc.want)
			}
		})
	}
}

func TestFetchCodeownersMissingAndBroken(t *testing.T) {
	t.Run("missing file means no owners", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		defer srv.Close()
		c, _ := testClient(srv)
		co, err := c.fetchCodeowners(t.Context(), "o/r", "release/v2")
		if err != nil || len(co.rules) != 0 {
			t.Errorf("fetchCodeowners() = %v, %v, want an empty file", co, err)
		}
	})
	t.Run("other errors fail", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "denied", http.StatusUnauthorized)
		}))
		defer srv.Close()
		c, _ := testClient(srv)
		if _, err := c.fetchCodeowners(t.Context(), "o/r", "release/v2"); err == nil {
			t.Error("fetchCodeowners() succeeded, want an error")
		}
	})
}

// fakeGitHub serves canned responses for repository o/r.
type fakeGitHub struct {
	t *testing.T

	mu        sync.Mutex
	issueBody string
	patches   []string

	// graphqlThreadsBody overrides the review-thread response when set.
	graphqlThreadsBody string

	// pullState overrides the state of pull request 5 when set.
	pullState string

	// mergeableNullCalls is the number of pull request 5 GETs that report
	// "mergeable": null before GitHub reports a value.
	mergeableNullCalls int

	// checkRunStartedAt maps check run names to their start times.
	checkRunStartedAt map[string]string

	pullGets       int
	codeownersRefs []string
}

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.t.Helper()
	if strings.HasPrefix(r.URL.Path, "/repos/o/r/contents/") {
		f.mu.Lock()
		f.codeownersRefs = append(f.codeownersRefs, r.URL.Query().Get("ref"))
		f.mu.Unlock()
	}
	write := func(v any) {
		if err := json.NewEncoder(w).Encode(v); err != nil {
			f.t.Error(err)
		}
	}
	switch path := r.URL.Path; {
	case path == "/repos/o/r/contents/.github/CODEOWNERS":
		// The lookup order tries this file first. dd-trace-go keeps CODEOWNERS
		// in the repository root.
		http.NotFound(w, r)
	case path == "/repos/o/r/contents/CODEOWNERS":
		write(map[string]string{"encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte("/appsec/ @DataDog/asm-go\n"))})
	case path == "/repos/o/r/pulls":
		write([]map[string]int{{"number": 5}, {"number": 6}})
	case path == "/repos/o/r/pulls/6":
		write(map[string]any{"number": 6, "user": map[string]string{"login": "dependabot[bot]", "type": "Bot"}})
	case path == "/repos/o/r/pulls/5":
		f.mu.Lock()
		f.pullGets++
		mergeable := any(true)
		if f.pullGets <= f.mergeableNullCalls {
			mergeable = nil
		}
		state := "open"
		if f.pullState != "" {
			state = f.pullState
		}
		f.mu.Unlock()
		write(map[string]any{
			"number": 5, "state": state, "title": "fix(appsec): thing", "html_url": "https://github.com/o/r/pull/5",
			"user": map[string]string{"login": "alice", "type": "User"}, "author_association": "CONTRIBUTOR",
			"mergeable": mergeable, "mergeable_state": "clean", "additions": 10, "deletions": 2, "changed_files": 1,
			"created_at": "2026-10-01T00:00:00Z", "updated_at": "2026-10-20T00:00:00Z",
			"head": map[string]string{"sha": "abc"}, "base": map[string]string{"ref": "main"},
			"labels":              []map[string]string{{"name": "bug"}},
			"assignees":           []map[string]string{{"login": "carol"}},
			"requested_reviewers": []map[string]string{{"login": "dave"}},
			"requested_teams":     []map[string]string{{"slug": "asm-go"}},
		})
	case path == "/repos/o/r/pulls/5/files":
		write([]map[string]string{{"filename": "appsec/waf.go"}})
	case path == "/repos/o/r/pulls/5/reviews":
		write([]map[string]any{
			{"user": map[string]string{"login": "bob", "type": "User"}, "state": "CHANGES_REQUESTED", "author_association": "MEMBER", "submitted_at": "2026-10-10T00:00:00Z"},
			{"user": map[string]string{"login": "copilot", "type": "Bot"}, "state": "COMMENTED", "author_association": "NONE", "submitted_at": "2026-10-11T00:00:00Z"},
		})
	case path == "/repos/o/r/issues/5/comments":
		write([]map[string]any{{"user": map[string]string{"login": "alice", "type": "User"}, "created_at": "2026-10-12T00:00:00Z"}})
	case path == "/repos/o/r/commits/abc/check-runs":
		runs := []map[string]string{
			{"name": "unit-tests", "status": "completed", "conclusion": "success"},
			{"name": "codecov/patch", "status": "completed", "conclusion": "failure"},
		}
		for _, run := range runs {
			if at, ok := f.checkRunStartedAt[run["name"]]; ok {
				run["started_at"] = at
			}
		}
		write(map[string]any{"total_count": 2, "check_runs": runs})
	case path == "/repos/o/r/commits/abc/status":
		write(map[string]any{"state": "success", "statuses": []map[string]string{{"context": "ci/legacy", "state": "success"}}})
	case path == "/repos/o/r/commits/abc/check-suites":
		write(map[string]any{"total_count": 1, "check_suites": []map[string]string{{"conclusion": "success"}}})
	case path == "/graphql":
		f.serveGraphQL(w, r)
	case path == "/repos/o/r/issues/42" && r.Method == http.MethodGet:
		f.mu.Lock()
		defer f.mu.Unlock()
		write(map[string]string{"body": f.issueBody})
	case path == "/repos/o/r/issues/42" && r.Method == http.MethodPatch:
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			f.t.Error(err)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.patches = append(f.patches, payload["body"])
		write(map[string]string{})
	default:
		f.t.Errorf("unexpected request %s %s", r.Method, r.URL)
		http.NotFound(w, r)
	}
}

func (f *fakeGitHub) serveGraphQL(w http.ResponseWriter, r *http.Request) {
	data, _ := io.ReadAll(r.Body)
	var req struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	if err := json.Unmarshal(data, &req); err != nil {
		f.t.Error(err)
	}
	switch {
	case strings.Contains(req.Query, "reviewThreads"):
		if req.Variables["number"] != float64(5) {
			f.t.Errorf("variables = %v, want number 5", req.Variables)
		}
		body := f.graphqlThreadsBody
		if body == "" {
			body = `{"data":{"repository":{"pullRequest":{"commits":{"nodes":[{"commit":{"committedDate":"2026-10-08T00:00:00Z","pushedDate":"2026-10-09T00:00:00Z"}}]},"reviewThreads":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[
				{"isResolved":false,"comments":{"nodes":[
					{"author":{"login":"bob","__typename":"User"},"authorAssociation":"MEMBER","body":"first","createdAt":"2026-10-09T10:00:00Z"},
					{"author":{"login":"Alice","__typename":"User"},"authorAssociation":"CONTRIBUTOR","body":"done","createdAt":"2026-10-09T12:00:00Z"},
					{"author":{"login":"bob","__typename":"User"},"authorAssociation":"MEMBER","body":"please fix","createdAt":"2026-10-10T01:00:00Z"}]}},
				{"isResolved":false,"comments":{"nodes":[{"author":{"login":"copilot-pull-request-reviewer","__typename":"Bot"},"authorAssociation":"NONE","body":"bot","createdAt":"2026-10-10T02:00:00Z"}]}},
				{"isResolved":true,"comments":{"nodes":[{"author":null,"authorAssociation":"NONE","body":"gone","createdAt":"2026-10-10T03:00:00Z"}]}}
			]}}}}}`
		}
		fmt.Fprint(w, body)
	case strings.Contains(req.Query, "totalCount"):
		fmt.Fprint(w, `{"data":{"repository":{"pullRequests":{"totalCount":57}}}}`)
	default:
		f.t.Errorf("unexpected query %s", req.Query)
	}
}

func TestGatherRepo(t *testing.T) {
	srv := httptest.NewServer(&fakeGitHub{t: t})
	defer srv.Close()
	c, _ := testClient(srv)

	prs, err := c.gatherRepo(t.Context(), "o/r", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 1 {
		t.Fatalf("gatherRepo() returned %d PRs, want 1 (the bot PR is skipped)", len(prs))
	}
	pr := prs[0]
	if pr.Number != 5 || pr.Author != "alice" || pr.BaseRef != "main" || pr.Mergeable == nil || !*pr.Mergeable ||
		pr.Additions != 10 || pr.Deletions != 2 || pr.Labels[0] != "bug" {
		t.Errorf("unexpected pull request fields: %+v", pr)
	}
	if got := fmt.Sprint(pr.RequestedReviewers, pr.Assignees); got != "[dave o/asm-go] [carol]" {
		t.Errorf("reviewers and assignees = %s", got)
	}
	if len(pr.Reviews) != 1 || pr.Reviews[0].Author != "bob" {
		t.Errorf("reviews = %+v, want only bob's review", pr.Reviews)
	}
	if len(pr.Comments) != 1 || len(pr.CheckRuns) != 2 || len(pr.Statuses) != 1 || pr.ActionRequiredSuites != 0 {
		t.Errorf("unexpected comments %d, check runs %d, statuses %d", len(pr.Comments), len(pr.CheckRuns), len(pr.Statuses))
	}
	if !pr.HeadCommitAt.Equal(time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)) {
		// The push time, not the commit time: the fake commit object is dated
		// 2026-10-08 but was pushed on 2026-10-09.
		t.Errorf("HeadCommitAt = %s", pr.HeadCommitAt)
	}
	if len(pr.Threads) != 2 || pr.Threads[0].LastAuthor != "bob" || pr.Threads[0].Resolved || pr.Threads[1].LastAuthor != "ghost" || !pr.Threads[1].Resolved {
		t.Errorf("threads = %+v, want bob's unresolved thread and a resolved ghost thread", pr.Threads)
	}
	if th := pr.Threads[0]; th.LastAuthorAssociation != "MEMBER" || !th.LastAt.Equal(time.Date(2026, 10, 10, 1, 0, 0, 0, time.UTC)) ||
		!th.AuthorLastAt.Equal(time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)) {
		// The thread ends with bob, but the reply of the author stays known.
		t.Errorf("thread = %+v, want bob's MEMBER comment last and the author reply on 2026-10-09T12:00:00Z", th)
	}
	if !pr.Threads[1].AuthorLastAt.IsZero() {
		t.Errorf("AuthorLastAt = %s, want zero for a thread without an author comment", pr.Threads[1].AuthorLastAt)
	}

	// The conversion must reach the same bucket as the rules: changes requested.
	if got := classify(pr); got.Bucket != BucketWaitingOnAuthor || got.CI.State != ciPass {
		t.Errorf("classify() = %s with CI %s, want %s with CI pass", got.Bucket, got.CI.State, BucketWaitingOnAuthor)
	}
}

func TestGatherRepoFallsBackToCommitDate(t *testing.T) {
	// A commit without a push time reports the commit time instead.
	override := `{"data":{"repository":{"pullRequest":{"commits":{"nodes":[{"commit":{"committedDate":"2026-10-08T00:00:00Z"}}]},"reviewThreads":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[]}}}}}`
	srv := httptest.NewServer(&fakeGitHub{t: t, graphqlThreadsBody: override})
	defer srv.Close()
	c, _ := testClient(srv)
	prs, err := c.gatherRepo(t.Context(), "o/r", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 1 {
		t.Fatalf("gatherRepo() returned %d PRs, want 1", len(prs))
	}
	if !prs[0].HeadCommitAt.Equal(time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("HeadCommitAt = %s, want the commit date when no push date exists", prs[0].HeadCommitAt)
	}
}

func TestGatherRepoFallsBackToFirstCheckRunStart(t *testing.T) {
	// Without a push time, the earliest check run start approximates the push.
	override := `{"data":{"repository":{"pullRequest":{"commits":{"nodes":[{"commit":{"committedDate":"2026-10-01T00:00:00Z","pushedDate":null}}]},"reviewThreads":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[]}}}}}`
	srv := httptest.NewServer(&fakeGitHub{t: t, graphqlThreadsBody: override, checkRunStartedAt: map[string]string{
		"unit-tests":    "2026-10-08T05:00:00Z",
		"codecov/patch": "2026-10-08T04:00:00Z",
	}})
	defer srv.Close()
	c, _ := testClient(srv)
	prs, err := c.gatherRepo(t.Context(), "o/r", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 1 {
		t.Fatalf("gatherRepo() returned %d PRs, want 1", len(prs))
	}
	if want := time.Date(2026, 10, 8, 4, 0, 0, 0, time.UTC); !prs[0].HeadCommitAt.Equal(want) {
		t.Errorf("HeadCommitAt = %s, want the earliest check run start %s", prs[0].HeadCommitAt, want)
	}
}

func TestGatherRepoRefetchesNullMergeable(t *testing.T) {
	fake := &fakeGitHub{t: t, mergeableNullCalls: 1}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	c, waits := testClient(srv)
	prs, err := c.gatherRepo(t.Context(), "o/r", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 1 || prs[0].Mergeable == nil || !*prs[0].Mergeable {
		t.Fatalf("gatherRepo() = %+v, want one PR with mergeable true", prs)
	}
	if fake.pullGets != 2 {
		t.Errorf("pull request GETs = %d, want 2 (the first one reports null)", fake.pullGets)
	}
	if fmt.Sprint(*waits) != fmt.Sprint([]time.Duration{2 * time.Second}) {
		t.Errorf("waits = %v, want one 2s wait before the refetch", *waits)
	}
}

func TestGatherRepoSkipsClosedPull(t *testing.T) {
	srv := httptest.NewServer(&fakeGitHub{t: t, pullState: "closed"})
	defer srv.Close()
	c, _ := testClient(srv)
	prs, err := c.gatherRepo(t.Context(), "o/r", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 0 {
		t.Errorf("gatherRepo() returned %d PRs, want 0 (pull request 5 is closed, 6 is a bot)", len(prs))
	}
}

func TestGatherRepoFailsLoudly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/o/r/pulls" {
			fmt.Fprint(w, `[{"number":5}]`)
			return
		}
		http.Error(w, "denied", http.StatusUnauthorized)
	}))
	defer srv.Close()
	c, _ := testClient(srv)
	_, err := c.gatherRepo(t.Context(), "o/r", 10)
	if err == nil || !strings.Contains(err.Error(), "o/r#5") || !strings.Contains(err.Error(), "401") {
		t.Errorf("gatherRepo() error = %v, want the PR and the status in it", err)
	}
}

func TestGraphQLErrorsFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"data":null,"errors":[{"message":"Could not resolve to a Repository"}]}`)
	}))
	defer srv.Close()
	c, _ := testClient(srv)
	_, err := c.openPullCount(t.Context(), "o/r")
	if err == nil || !strings.Contains(err.Error(), "Could not resolve") {
		t.Errorf("openPullCount() error = %v, want the GraphQL message", err)
	}
}

func TestGraphQLRetriesTransientErrors(t *testing.T) {
	tests := []struct {
		name      string
		errors    []string // error message per call, empty means success
		wantErr   string
		wantCalls int
		wantWaits []time.Duration
	}{
		{
			name:      "transient error then success",
			errors:    []string{"Something went wrong while executing your query. Please try again later."},
			wantCalls: 2,
			wantWaits: []time.Duration{time.Second},
		},
		{
			name:      "timeout then success",
			errors:    []string{"Request TIMEOUT", "Request timeout"},
			wantCalls: 3,
			wantWaits: []time.Duration{time.Second, 2 * time.Second},
		},
		{
			name:      "persistent transient error gives up",
			errors:    []string{"please try again", "please try again", "please try again", "please try again"},
			wantErr:   "giving up after 4 attempts",
			wantCalls: 4,
			wantWaits: []time.Duration{time.Second, 2 * time.Second, 4 * time.Second},
		},
		{
			name:      "other error fails at once",
			errors:    []string{"Could not resolve to a Repository"},
			wantErr:   "Could not resolve",
			wantCalls: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				if calls <= len(tc.errors) {
					msg, _ := json.Marshal(tc.errors[calls-1])
					fmt.Fprintf(w, `{"data":null,"errors":[{"message":%s}]}`, msg)
					return
				}
				fmt.Fprint(w, `{"data":{"repository":{"pullRequests":{"totalCount":3}}}}`)
			}))
			defer srv.Close()
			c, waits := testClient(srv)
			got, err := c.openPullCount(t.Context(), "o/r")
			if tc.wantErr == "" && (err != nil || got != 3) {
				t.Fatalf("openPullCount() = %d, %v, want 3", got, err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
			}
			if calls != tc.wantCalls {
				t.Errorf("calls = %d, want %d", calls, tc.wantCalls)
			}
			if fmt.Sprint(*waits) != fmt.Sprint(tc.wantWaits) {
				t.Errorf("waits = %v, want %v", *waits, tc.wantWaits)
			}
		})
	}
}

func TestSplitRepo(t *testing.T) {
	for _, repo := range []string{"", "o", "o/", "/r", "o/r/x"} {
		if _, _, err := splitRepo(repo); err == nil {
			t.Errorf("splitRepo(%q) succeeded, want an error", repo)
		}
	}
	if owner, name, err := splitRepo("o/r"); err != nil || owner != "o" || name != "r" {
		t.Errorf("splitRepo(o/r) = %q, %q, %v", owner, name, err)
	}
}

func TestRun(t *testing.T) {
	fake := &fakeGitHub{t: t}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	c, _ := testClient(srv)
	dir := t.TempDir()
	cfg := config{
		repos:             []string{"o/r"},
		limit:             10,
		issue:             42,
		output:            filepath.Join(dir, "board.md"),
		classificationOut: filepath.Join(dir, "request.json"),
	}

	if err := run(t.Context(), cfg, c, io.Discard, testNow); err != nil {
		t.Fatal(err)
	}
	board, err := os.ReadFile(cfg.output)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Analyzed `1` non-bot", "[#5](https://github.com/o/r/pull/5)", "(57)", "`@alice` (comment)", "--repo o/r --limit 10"} {
		if !strings.Contains(string(board), want) {
			t.Errorf("board lacks %q", want)
		}
	}
	for _, ref := range fake.codeownersRefs {
		if ref != "main" {
			t.Errorf("CODEOWNERS ref = %q, want the base branch main", ref)
		}
	}
	if len(fake.codeownersRefs) == 0 {
		t.Error("run() did not fetch CODEOWNERS")
	}
	if len(fake.patches) != 1 || fake.patches[0] != string(board) {
		t.Fatalf("issue patches = %d, want one patch with the board", len(fake.patches))
	}

	var records []map[string]any
	data, err := os.ReadFile(cfg.classificationOut)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &records); err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0]["number"] != float64(5) || records[0]["deterministic_bucket"] != string(BucketWaitingOnAuthor) {
		t.Errorf("classification request = %s", data)
	}
	for _, key := range []string{"title", "author", "state", "draft", "mergeable", "ci", "review_threads", "reviews", "labels", "additions", "deletions"} {
		if _, ok := records[0][key]; !ok {
			t.Errorf("classification record lacks %q", key)
		}
	}

	// A second run with the same data and a later snapshot time must not touch the issue.
	fake.issueBody = fake.patches[0]
	if err := run(t.Context(), cfg, c, io.Discard, testNow.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if len(fake.patches) != 1 {
		t.Errorf("issue patches = %d after an unchanged run, want 1", len(fake.patches))
	}
}

func TestRunWritesStdoutByDefault(t *testing.T) {
	srv := httptest.NewServer(&fakeGitHub{t: t})
	defer srv.Close()
	c, _ := testClient(srv)
	var out bytes.Buffer
	if err := run(t.Context(), config{repos: []string{"o/r"}, limit: 10}, c, &out, testNow); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "## Queue Health") {
		t.Errorf("stdout lacks the board: %q", out.String())
	}
}

func TestRunRejectsBadInput(t *testing.T) {
	c := newClient("t")
	for name, cfg := range map[string]config{
		"bad repo":  {repos: []string{"nope"}, limit: 1},
		"bad limit": {repos: []string{"o/r"}, limit: 0},
	} {
		if err := run(t.Context(), cfg, c, io.Discard, testNow); err == nil {
			t.Errorf("%s: run() succeeded, want an error", name)
		}
	}
}

func TestUpdateIssueRejectsOversizedBoard(t *testing.T) {
	err := updateIssue(t.Context(), newClient("t"), "o/r", 1, strings.Repeat("x", maxIssueBodyChars+1))
	if err == nil || !strings.Contains(err.Error(), "lower --limit") {
		t.Errorf("updateIssue() error = %v, want the size error", err)
	}
}

func TestNotFoundError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	c, _ := testClient(srv)
	err := c.getJSON(t.Context(), "/missing", &struct{}{})
	if nf := (*notFoundError)(nil); !errors.As(err, &nf) {
		t.Errorf("error = %v, want a notFoundError", err)
	}
}

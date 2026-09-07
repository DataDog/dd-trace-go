// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeClock struct {
	now    time.Time
	sleeps []time.Duration
}

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) Sleep(ctx context.Context, duration time.Duration) error {
	c.sleeps = append(c.sleeps, duration)
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func TestProductionGitHubClientUsesFixedAPIOrigin(t *testing.T) {
	client := NewProductionGitHubClient("token")
	if client.baseURL.String() != ProductionGitHubAPIEndpoint {
		t.Fatalf("baseURL = %q, want %q", client.baseURL.String(), ProductionGitHubAPIEndpoint)
	}
	if got := client.urlForPath(issueCommentsPathPrefix+"456/comments", nil).String(); !strings.HasPrefix(got, ProductionGitHubAPIEndpoint+issueCommentsPathPrefix) {
		t.Fatalf("unexpected production URL: %s", got)
	}
}

func TestListIssueCommentsRejectsHostileRedirectWithoutFollowing(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") == "" {
			t.Fatal("missing authorization on approved origin request")
		}
		w.Header().Set("Location", "https://evil.example/repos/DataDog/dd-trace-go/issues/456/comments?page=2")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	client, err := newTestGitHubClient(server.URL, server.Client(), &fakeClock{now: time.Unix(100, 0)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ListIssueComments(context.Background(), "456")
	if ErrorCode(err) != "unexpected_api_origin" {
		t.Fatalf("error = %q, want unexpected_api_origin", ErrorCode(err))
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want one request with no hostile follow", requests)
	}
}

func TestListIssueCommentsRejectsHostilePaginationLink(t *testing.T) {
	cases := []struct {
		name string
		link string
		code string
	}{
		{name: "other origin", link: `<https://evil.example/repos/DataDog/dd-trace-go/issues/456/comments?page=2>; rel="next"`, code: "unexpected_api_origin"},
		{name: "same origin wrong endpoint", link: `</repos/DataDog/dd-trace-go/issues/456/comments_evil?page=2>; rel="next"`, code: "unexpected_api_path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Link", tc.link)
				_, _ = w.Write([]byte(`[]`))
			}))
			defer server.Close()
			client, err := newTestGitHubClient(server.URL, server.Client(), &fakeClock{now: time.Unix(100, 0)})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.ListIssueComments(context.Background(), "456")
			if ErrorCode(err) != tc.code {
				t.Fatalf("error = %q, want %q", ErrorCode(err), tc.code)
			}
		})
	}
}

func TestListIssueCommentsPaginatesWithinEndpointFamily(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "1":
			w.Header().Set("Link", `</repos/DataDog/dd-trace-go/issues/456/comments?per_page=100&page=2>; rel="next"`)
			_, _ = w.Write([]byte(`[{"id":1,"body":"first","author_association":"MEMBER","user":{"login":"a"}}]`))
		case "2":
			_, _ = w.Write([]byte(`[{"id":"2","body":"second","author_association":"OWNER","user":{"login":"b"}}]`))
		default:
			t.Fatalf("unexpected page %q", r.URL.Query().Get("page"))
		}
	}))
	defer server.Close()
	client, err := newTestGitHubClient(server.URL, server.Client(), &fakeClock{now: time.Unix(100, 0)})
	if err != nil {
		t.Fatal(err)
	}
	comments, err := client.ListIssueComments(context.Background(), "456")
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 2 || comments[0].ID != "1" || comments[1].ID != "2" {
		t.Fatalf("unexpected comments: %#v", comments)
	}
}

func TestListIssueCommentsFailsClosedAtPageCap(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", `</repos/DataDog/dd-trace-go/issues/456/comments?per_page=100&page=2>; rel="next"`)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	client, err := newTestGitHubClient(server.URL, server.Client(), &fakeClock{now: time.Unix(100, 0)})
	if err != nil {
		t.Fatal(err)
	}
	client.maxPages = 1
	_, err = client.ListIssueComments(context.Background(), "456")
	if ErrorCode(err) != "pagination_exhausted" {
		t.Fatalf("error = %q, want pagination_exhausted", ErrorCode(err))
	}
}

func TestListIssueCommentsBoundsResponseSize(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":1,"body":"too large","author_association":"MEMBER","user":{"login":"a"}}]`))
	}))
	defer server.Close()
	client, err := newTestGitHubClient(server.URL, server.Client(), &fakeClock{now: time.Unix(100, 0)})
	if err != nil {
		t.Fatal(err)
	}
	client.maxBytes = 2
	_, err = client.ListIssueComments(context.Background(), "456")
	if ErrorCode(err) != "response_too_large" {
		t.Fatalf("error = %q, want response_too_large", ErrorCode(err))
	}
}

func TestListIssueCommentsRetriesRateLimitWithinBounds(t *testing.T) {
	attempts := 0
	clock := &fakeClock{now: time.Unix(100, 0)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", "101")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	client, err := newTestGitHubClient(server.URL, server.Client(), clock)
	if err != nil {
		t.Fatal(err)
	}
	comments, err := client.ListIssueComments(context.Background(), "456")
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 0 || attempts != 2 || len(clock.sleeps) == 0 {
		t.Fatalf("comments=%#v attempts=%d sleeps=%v", comments, attempts, clock.sleeps)
	}
}

func TestListIssueCommentsTimeoutFailsClosed(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	client, err := newTestGitHubClient(server.URL, server.Client(), &fakeClock{now: time.Unix(100, 0)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.ListIssueComments(ctx, "456")
	if ErrorCode(err) != "request_timeout" {
		t.Fatalf("error = %q, want request_timeout", ErrorCode(err))
	}
	if requests != 0 {
		t.Fatalf("requests = %d, want no request after canceled context", requests)
	}
}

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const issueCommentsPathPrefix = "/repos/DataDog/dd-trace-go/issues/"

type GitHubClient struct {
	baseURL      *url.URL
	httpClient   *http.Client
	clock        Clock
	token        string
	maxBytes     int64
	maxPages     int
	perPage      int
	retries      int
	readDeadline time.Duration
}

type IssueComment struct {
	ID                string
	Body              string
	AuthorLogin       string
	AuthorAssociation string
}

func NewProductionGitHubClient(token string) *GitHubClient {
	base, err := url.Parse(ProductionGitHubAPIEndpoint)
	if err != nil {
		panic(err)
	}
	return &GitHubClient{
		baseURL: base,
		httpClient: &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		clock:        RealClock{},
		token:        token,
		maxBytes:     MaxGitHubResponseBytes,
		maxPages:     MaxGitHubPages,
		perPage:      GitHubPageSize,
		retries:      MaxReadRetries,
		readDeadline: time.Minute,
	}
}

func newTestGitHubClient(base string, client *http.Client, clock Clock) (*GitHubClient, error) {
	parsed, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = &http.Client{}
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	if clock == nil {
		clock = RealClock{}
	}
	return &GitHubClient{
		baseURL:      parsed,
		httpClient:   client,
		clock:        clock,
		token:        "test-token",
		maxBytes:     MaxGitHubResponseBytes,
		maxPages:     MaxGitHubPages,
		perPage:      GitHubPageSize,
		retries:      MaxReadRetries,
		readDeadline: time.Minute,
	}, nil
}

func (c *GitHubClient) ListIssueComments(ctx context.Context, issueNumber string) ([]IssueComment, error) {
	if !validID(issueNumber) {
		return nil, typedRequestError("unsafe_id")
	}
	path := issueCommentsPathPrefix + issueNumber + "/comments"
	values := url.Values{}
	values.Set("per_page", strconv.Itoa(c.perPage))
	values.Set("page", "1")
	nextURL := c.urlForPath(path, values)
	comments := []IssueComment{}
	for page := 1; ; page++ {
		if page > c.maxPages {
			return nil, newReleaseError(ErrorClassEvidenceIncomplete, "pagination_exhausted")
		}
		body, header, err := c.getBounded(ctx, nextURL, path)
		if err != nil {
			return nil, err
		}
		pageComments, err := decodeIssueComments(body)
		if err != nil {
			return nil, err
		}
		comments = append(comments, pageComments...)
		next, hasNext, err := c.nextPageURL(nextURL, path, header.Get("Link"))
		if err != nil {
			return nil, err
		}
		if !hasNext {
			return comments, nil
		}
		nextURL = next
	}
}

func (c *GitHubClient) urlForPath(path string, query url.Values) *url.URL {
	result := *c.baseURL
	result.Path = path
	result.RawQuery = query.Encode()
	return &result
}

func (c *GitHubClient) getBounded(ctx context.Context, endpoint *url.URL, expectedPath string) ([]byte, http.Header, error) {
	var lastErr error
	for attempt := 0; attempt <= c.retries; attempt++ {
		if attempt > 0 {
			if err := c.clock.Sleep(ctx, time.Duration(attempt)*time.Second); err != nil {
				return nil, nil, wrapReleaseError(ErrorClassEvidenceIncomplete, "request_timeout", err)
			}
		}
		attemptCtx := ctx
		cancel := func() {}
		if c.readDeadline > 0 {
			attemptCtx, cancel = context.WithTimeout(ctx, c.readDeadline)
		}
		body, header, retry, err := c.getOnce(attemptCtx, endpoint, expectedPath)
		cancel()
		if err == nil {
			return body, header, nil
		}
		lastErr = err
		if !retry {
			return nil, nil, err
		}
	}
	return nil, nil, wrapReleaseError(ErrorClassEvidenceIncomplete, "read_retries_exhausted", lastErr)
}

func (c *GitHubClient) getOnce(ctx context.Context, endpoint *url.URL, expectedPath string) ([]byte, http.Header, bool, error) {
	if err := c.validateEndpoint(endpoint, expectedPath); err != nil {
		return nil, nil, false, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, nil, false, wrapReleaseError(ErrorClassEvidenceIncomplete, "request_build_failed", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(ctx.Err(), context.Canceled) {
			return nil, nil, false, wrapReleaseError(ErrorClassEvidenceIncomplete, "request_timeout", ctx.Err())
		}
		return nil, nil, true, wrapReleaseError(ErrorClassEvidenceIncomplete, "transport_error", err)
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		if _, err := c.resolveBoundedURL(endpoint, expectedPath, response.Header.Get("Location")); err != nil {
			return nil, nil, false, err
		}
		return nil, nil, false, newReleaseError(ErrorClassEvidenceIncomplete, "redirect_not_followed")
	}
	if response.StatusCode == http.StatusForbidden && response.Header.Get("X-RateLimit-Remaining") == "0" {
		if delay := rateLimitDelay(c.clock.Now(), response.Header.Get("X-RateLimit-Reset")); delay > 0 {
			if err := c.clock.Sleep(ctx, delay); err != nil {
				return nil, nil, false, wrapReleaseError(ErrorClassEvidenceIncomplete, "request_timeout", err)
			}
			return nil, nil, true, newReleaseError(ErrorClassEvidenceIncomplete, "rate_limited")
		}
	}
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
		return nil, nil, true, newReleaseError(ErrorClassEvidenceIncomplete, "retryable_status")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, nil, false, newReleaseError(ErrorClassEvidenceIncomplete, "unexpected_status")
	}
	reader := io.LimitReader(response.Body, c.maxBytes+1)
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, nil, false, wrapReleaseError(ErrorClassEvidenceIncomplete, "response_read_failed", err)
	}
	if int64(len(body)) > c.maxBytes {
		return nil, nil, false, newReleaseError(ErrorClassEvidenceIncomplete, "response_too_large")
	}
	return body, response.Header.Clone(), false, nil
}

func (c *GitHubClient) validateEndpoint(endpoint *url.URL, expectedPath string) error {
	if endpoint.Scheme != c.baseURL.Scheme || endpoint.Host != c.baseURL.Host {
		return newReleaseError(ErrorClassEvidenceIncomplete, "unexpected_api_origin")
	}
	if endpoint.Path != expectedPath {
		return newReleaseError(ErrorClassEvidenceIncomplete, "unexpected_api_path")
	}
	return nil
}

func (c *GitHubClient) nextPageURL(current *url.URL, expectedPath, linkHeader string) (*url.URL, bool, error) {
	for _, part := range strings.Split(linkHeader, ",") {
		sections := strings.Split(part, ";")
		if len(sections) < 2 {
			continue
		}
		if !strings.Contains(strings.Join(sections[1:], ";"), `rel="next"`) {
			continue
		}
		raw := strings.TrimSpace(sections[0])
		if len(raw) < 2 || raw[0] != '<' || raw[len(raw)-1] != '>' {
			return nil, false, newReleaseError(ErrorClassEvidenceIncomplete, "invalid_pagination_link")
		}
		next, err := c.resolveBoundedURL(current, expectedPath, raw[1:len(raw)-1])
		if err != nil {
			return nil, false, err
		}
		return next, true, nil
	}
	return nil, false, nil
}

func (c *GitHubClient) resolveBoundedURL(current *url.URL, expectedPath, rawLocation string) (*url.URL, error) {
	if rawLocation == "" {
		return nil, newReleaseError(ErrorClassEvidenceIncomplete, "missing_location")
	}
	location, err := url.Parse(rawLocation)
	if err != nil {
		return nil, wrapReleaseError(ErrorClassEvidenceIncomplete, "invalid_location", err)
	}
	resolved := current.ResolveReference(location)
	if err := c.validateEndpoint(resolved, expectedPath); err != nil {
		return nil, err
	}
	return resolved, nil
}

func decodeIssueComments(raw []byte) ([]IssueComment, error) {
	var values []struct {
		ID                json.RawMessage `json:"id"`
		Body              string          `json:"body"`
		AuthorAssociation string          `json:"author_association"`
		User              struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, typedContractError("invalid_api_json")
	}
	comments := make([]IssueComment, 0, len(values))
	for _, value := range values {
		id, err := decodeGitHubID(value.ID)
		if err != nil {
			return nil, err
		}
		comments = append(comments, IssueComment{ID: id, Body: value.Body, AuthorLogin: value.User.Login, AuthorAssociation: value.AuthorAssociation})
	}
	return comments, nil
}

func decodeGitHubID(raw json.RawMessage) (string, error) {
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		id := number.String()
		if !validID(id) {
			return "", typedRequestError("unsafe_id")
		}
		return id, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil && validID(s) {
		return s, nil
	}
	return "", typedRequestError("unsafe_id")
}

func rateLimitDelay(now time.Time, reset string) time.Duration {
	seconds, err := strconv.ParseInt(reset, 10, 64)
	if err != nil || seconds <= 0 {
		return 0
	}
	resetAt := time.Unix(seconds, 0)
	if !resetAt.After(now) {
		return 0
	}
	return resetAt.Sub(now)
}

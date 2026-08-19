// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package export

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protowire"
)

type stubRoundTripper struct {
	status int
	header http.Header
	body   io.ReadCloser
	err    error
}

func (s stubRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	if s.err != nil {
		return nil, s.err
	}
	headers := s.header
	if headers == nil {
		headers = http.Header{}
	}
	return &http.Response{StatusCode: s.status, Header: headers, Body: s.body}, nil
}

type errReadCloser struct{}

func (errReadCloser) Read([]byte) (int, error) { return 0, errors.New("connection reset") }
func (errReadCloser) Close() error             { return nil }

func stubTransport(roundTripper http.RoundTripper) *rawTransport {
	endpoint, _ := url.Parse("http://x")
	return &rawTransport{
		client:                &http.Client{Transport: roundTripper},
		endpoint:              endpoint,
		headers:               http.Header{},
		maxAttempts:           1,
		defaultRequestTimeout: 10 * time.Second,
	}
}

func TestDoPost_ReadErrorOnSuccess(t *testing.T) {
	transport := stubTransport(stubRoundTripper{status: http.StatusOK, body: errReadCloser{}})
	attempt := transport.doPost(context.Background(), pathTraces, []byte("payload"))
	require.Error(t, attempt.err)
	assert.Equal(t, 0, attempt.statusCode)
	assert.True(t, otlpRetriable(context.Background(), attempt.statusCode))
}

type countingReadCloser struct {
	reader io.Reader
	read   *int
}

func (c countingReadCloser) Read(buffer []byte) (int, error) {
	read, err := c.reader.Read(buffer)
	*c.read += read
	return read, err
}
func (countingReadCloser) Close() error { return nil }

func TestDoPost_DrainsResponse(t *testing.T) {
	const size = responseLimit + 512
	read := 0
	transport := stubTransport(stubRoundTripper{
		status: http.StatusOK,
		body:   countingReadCloser{reader: bytes.NewReader(make([]byte, size)), read: &read},
	})
	attempt := transport.doPost(context.Background(), pathTraces, []byte("payload"))
	require.ErrorIs(t, attempt.err, errResponseTooLarge)
	assert.Equal(t, http.StatusOK, attempt.statusCode)
	assert.True(t, attempt.terminal)
	assert.Equal(t, size, read)
}

func TestSubmitBody_DoesNotRetryOversizedResponse(t *testing.T) {
	transport := stubTransport(stubRoundTripper{
		status: http.StatusServiceUnavailable,
		body:   io.NopCloser(bytes.NewReader(make([]byte, responseLimit+1))),
	})
	transport.maxAttempts = 3

	result, err := transport.submitBody(context.Background(), pathTraces, []byte("payload"))
	require.ErrorIs(t, err, errResponseTooLarge)
	assert.Equal(t, http.StatusServiceUnavailable, result.statusCode)
	assert.Equal(t, 1, result.attempts)
	assert.False(t, result.retriable)
}

func TestResolveClientConfig_EnforcesNoRedirect(t *testing.T) {
	custom := &http.Client{}
	cfg, err := resolveClientConfig([]ClientOption{
		WithCollectorEndpoint("http://collector:4318"),
		WithHTTPClient(custom),
	})
	require.NoError(t, err)
	require.NotNil(t, cfg.httpClient.CheckRedirect)
	assert.ErrorIs(t, cfg.httpClient.CheckRedirect(nil, nil), http.ErrUseLastResponse)
	assert.Nil(t, custom.CheckRedirect)

	sentinel := errors.New("caller policy")
	custom = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return sentinel }}
	cfg, err = resolveClientConfig([]ClientOption{
		WithCollectorEndpoint("http://collector:4318"),
		WithHTTPClient(custom),
	})
	require.NoError(t, err)
	assert.ErrorIs(t, cfg.httpClient.CheckRedirect(nil, nil), http.ErrUseLastResponse)
	assert.ErrorIs(t, custom.CheckRedirect(nil, nil), sentinel)
}

func TestResolveClientConfig_MaxRequestSize(t *testing.T) {
	cfg, err := resolveClientConfig([]ClientOption{WithCollectorEndpoint("http://collector:4318")})
	require.NoError(t, err)
	assert.Equal(t, defaultMaxRequestSize, cfg.maxRequestSize)

	cfg, err = resolveClientConfig([]ClientOption{
		WithCollectorEndpoint("http://collector:4318"),
		WithMaxRequestSize(-1),
	})
	require.NoError(t, err)
	assert.Equal(t, -1, cfg.maxRequestSize)
}

type deadlineCapture struct {
	capture func(remaining time.Duration, ok bool)
}

func (d deadlineCapture) RoundTrip(request *http.Request) (*http.Response, error) {
	deadline, ok := request.Context().Deadline()
	remaining := time.Duration(0)
	if ok {
		remaining = time.Until(deadline)
	}
	d.capture(remaining, ok)
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
}

func TestDoPost_RequestTimeout(t *testing.T) {
	var remaining time.Duration
	var hasDeadline bool
	roundTripper := deadlineCapture{capture: func(value time.Duration, ok bool) { remaining, hasDeadline = value, ok }}
	newTransport := func(requestTimeout time.Duration) *rawTransport {
		transport := stubTransport(roundTripper)
		transport.requestTimeout = requestTimeout
		return transport
	}

	newTransport(5*time.Second).doPost(context.Background(), pathTraces, []byte("x"))
	require.True(t, hasDeadline)
	assert.InDelta(t, 5.0, remaining.Seconds(), 1.0)

	newTransport(0).doPost(context.Background(), pathTraces, []byte("x"))
	require.True(t, hasDeadline)
	assert.InDelta(t, 10.0, remaining.Seconds(), 1.0)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	newTransport(0).doPost(ctx, pathTraces, []byte("x"))
	require.True(t, hasDeadline)
	assert.Greater(t, remaining.Seconds(), 20.0)
}

func TestDoPost_RequiresStatusOK(t *testing.T) {
	for _, status := range []int{http.StatusAccepted, http.StatusNoContent, http.StatusPartialContent, http.StatusFound} {
		transport := stubTransport(stubRoundTripper{status: status, body: io.NopCloser(strings.NewReader(""))})
		attempt := transport.doPost(context.Background(), pathTraces, []byte("payload"))
		require.Errorf(t, attempt.err, "status %d should fail", status)
		assert.Equal(t, status, attempt.statusCode)
	}
}

func TestDoPost_RetryAfter(t *testing.T) {
	transport := stubTransport(stubRoundTripper{
		status: http.StatusServiceUnavailable,
		header: http.Header{"Retry-After": []string{"2"}},
		body:   io.NopCloser(strings.NewReader("busy")),
	})
	attempt := transport.doPost(context.Background(), pathTraces, []byte("payload"))
	require.Error(t, attempt.err)
	assert.Equal(t, 2*time.Second, attempt.retryAfter)
}

func TestSubmitBody_CancelInterruptsRetryAfter(t *testing.T) {
	transport := stubTransport(stubRoundTripper{
		status: http.StatusServiceUnavailable,
		header: http.Header{"Retry-After": []string{"60"}},
		body:   io.NopCloser(strings.NewReader("busy")),
	})
	transport.maxAttempts = 3
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	start := time.Now()

	result, err := transport.submitBody(ctx, pathTraces, []byte("payload"))
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, result.attempts)
	assert.False(t, result.retriable)
	assert.Less(t, time.Since(start), time.Second)
}

type failOnceRoundTripper struct {
	attempts int
}

func (f *failOnceRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	f.attempts++
	if f.attempts == 1 {
		return nil, errors.New("connection refused")
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
}

func TestSubmitBody_RetriesNetworkFailure(t *testing.T) {
	roundTripper := &failOnceRoundTripper{}
	transport := stubTransport(roundTripper)
	transport.maxAttempts = 2

	result, err := transport.submitBody(context.Background(), pathTraces, []byte("payload"))
	require.NoError(t, err)
	assert.Equal(t, 2, result.attempts)
	assert.Equal(t, 2, roundTripper.attempts)
	assert.False(t, result.retriable)
}

func TestOTLPRetriable(t *testing.T) {
	for status, want := range map[int]bool{
		0:   true,
		429: true,
		502: true,
		503: true,
		504: true,
		408: false,
		500: false,
		400: false,
		200: false,
	} {
		assert.Equalf(t, want, otlpRetriable(context.Background(), status), "status %d", status)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	assert.False(t, otlpRetriable(canceled, http.StatusServiceUnavailable))
}

func TestParseRetryAfter(t *testing.T) {
	headers := func(value string) http.Header { return http.Header{"Retry-After": []string{value}} }
	assert.Zero(t, parseRetryAfter(http.Header{}))
	assert.Equal(t, 5*time.Second, parseRetryAfter(headers("5")))
	assert.Zero(t, parseRetryAfter(headers("-3")))
	assert.Equal(t, maxRetryAfter, parseRetryAfter(headers("100000")))
	assert.Equal(t, maxRetryAfter, parseRetryAfter(headers("9223372037")))
	assert.Zero(t, parseRetryAfter(headers("soon")))

	future := time.Now().Add(3 * time.Second).UTC().Format(http.TimeFormat)
	delay := parseRetryAfter(headers(future))
	assert.Positive(t, delay)
	assert.LessOrEqual(t, delay, maxRetryAfter)
	assert.Zero(t, parseRetryAfter(headers(time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat))))
}

func TestOTLPStatusMessage(t *testing.T) {
	body := protowire.AppendTag(nil, 1, protowire.VarintType)
	body = protowire.AppendVarint(body, 3)
	body = protowire.AppendTag(body, 2, protowire.BytesType)
	body = protowire.AppendBytes(body, []byte("invalid trace_id length"))
	assert.Equal(t, "invalid trace_id length", otlpStatusMessage(body))
	assert.Empty(t, otlpStatusMessage([]byte{0xff, 0xff, 0xff}))
	assert.Empty(t, otlpStatusMessage(nil))
}

func TestResponseSnippet(t *testing.T) {
	assert.Equal(t, "boom", responseSnippet([]byte("  boom \n")))
	assert.Empty(t, responseSnippet(nil))
	assert.Len(t, responseSnippet([]byte(strings.Repeat("a", responseSnippetMaxBytes+100))), responseSnippetMaxBytes)
	assert.True(t, utf8.ValidString(responseSnippet([]byte(strings.Repeat("é", responseSnippetMaxBytes)))))
	assert.Equal(t, "ok", responseSnippet([]byte{'o', 'k', 0xff, 0xfe}))
}

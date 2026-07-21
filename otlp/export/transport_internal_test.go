// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package export

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protowire"
)

// stubRoundTripper returns a canned response (or error) without a network.
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
	h := s.header
	if h == nil {
		h = http.Header{}
	}
	return &http.Response{StatusCode: s.status, Header: h, Body: s.body}, nil
}

// errReadCloser fails on the first Read, simulating a reset connection after the
// status/headers were received.
type errReadCloser struct{}

func (errReadCloser) Read([]byte) (int, error) { return 0, errors.New("connection reset") }
func (errReadCloser) Close() error             { return nil }

func stubTransport(rt http.RoundTripper) *rawTransport {
	return &rawTransport{client: &http.Client{Transport: rt}, endpoint: "http://x/v1/traces", headers: http.Header{}, maxAttempts: 1}
}

func TestDoPost_ReadErrorOn2xxIsSurfacedAsRetryableFailure(t *testing.T) {
	tr := stubTransport(stubRoundTripper{status: 200, body: errReadCloser{}})
	a := tr.doPost(context.Background(), []byte("payload"))
	require.Error(t, a.Err) // an unreadable 2xx body must not be reported as success
	assert.Equal(t, 0, a.Status)
	assert.True(t, otlpRetriable(context.Background(), a.Status)) // treated as transport-class -> retryable
}

func TestDefaultHTTPClient_DoesNotFollowRedirects(t *testing.T) {
	c := defaultHTTPClient()
	require.NotNil(t, c.CheckRedirect) // must not use Go's default follow-redirect policy
	// A redirect must surface the 3xx instead of replaying the POST as a GET
	// (dropped body) or forwarding dd-api-key to the redirect target.
	assert.ErrorIs(t, c.CheckRedirect(nil, nil), http.ErrUseLastResponse)
}

func TestNewRawTransport_EnforcesNoRedirectOnCustomClient(t *testing.T) {
	// A caller-provided client without its own redirect policy gets no-redirect
	// enforced (so dd-api-key is never forwarded and a redirect is a failed export).
	custom := &http.Client{}
	tr, err := newRawTransport(Config{Site: "datadoghq.com", APIKey: "k", HTTPClient: custom}, pathTraces, nil)
	require.NoError(t, err)
	require.NotNil(t, tr.client.CheckRedirect)
	assert.ErrorIs(t, tr.client.CheckRedirect(nil, nil), http.ErrUseLastResponse)
	assert.Nil(t, custom.CheckRedirect, "caller's client must not be mutated")

	// A caller that sets its own redirect policy is respected, not overridden.
	sentinel := errors.New("caller policy")
	custom2 := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return sentinel }}
	tr2, err := newRawTransport(Config{Site: "datadoghq.com", APIKey: "k", HTTPClient: custom2}, pathTraces, nil)
	require.NoError(t, err)
	assert.ErrorIs(t, tr2.client.CheckRedirect(nil, nil), sentinel)
}

// deadlineCapture records the deadline seen by the outgoing request's context.
type deadlineCapture struct {
	dur func(remaining time.Duration, ok bool)
}

func (d deadlineCapture) RoundTrip(req *http.Request) (*http.Response, error) {
	dl, ok := req.Context().Deadline()
	rem := time.Duration(0)
	if ok {
		rem = time.Until(dl)
	}
	d.dur(rem, ok)
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
}

func TestDoPost_RequestTimeout(t *testing.T) {
	var rem time.Duration
	var hasDeadline bool
	rt := deadlineCapture{dur: func(r time.Duration, ok bool) { rem, hasDeadline = r, ok }}
	mk := func(reqTimeout time.Duration) *rawTransport {
		return &rawTransport{client: &http.Client{Transport: rt}, endpoint: "http://x/v1/traces", headers: http.Header{}, maxAttempts: 1, requestTimeout: reqTimeout}
	}

	// Explicit RequestTimeout is applied to the attempt.
	mk(5*time.Second).doPost(context.Background(), []byte("x"))
	require.True(t, hasDeadline)
	assert.InDelta(t, 5.0, rem.Seconds(), 1.0)

	// RequestTimeout==0, no caller deadline -> the 10s default is applied.
	mk(0).doPost(context.Background(), []byte("x"))
	require.True(t, hasDeadline)
	assert.InDelta(t, 10.0, rem.Seconds(), 1.0)

	// RequestTimeout==0 with a longer caller deadline -> respected, not shortened.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	mk(0).doPost(ctx, []byte("x"))
	require.True(t, hasDeadline)
	assert.Greater(t, rem.Seconds(), 20.0)
}

func TestDoPost_Non200IsFailure(t *testing.T) {
	// 202/204/206 are 2xx but not the OTLP success contract (200 + proto body);
	// a not-followed redirect (302) must also be a failure.
	for _, code := range []int{202, 204, 206, 302} {
		tr := stubTransport(stubRoundTripper{status: code, body: io.NopCloser(strings.NewReader(""))})
		a := tr.doPost(context.Background(), []byte("payload"))
		require.Errorf(t, a.Err, "status %d should be a failed export", code)
		assert.Equal(t, code, a.Status)
	}
}

func TestDoPost_ThreadsRetryAfterHeader(t *testing.T) {
	tr := stubTransport(stubRoundTripper{
		status: 503,
		header: http.Header{"Retry-After": []string{"2"}},
		body:   io.NopCloser(strings.NewReader("busy")),
	})
	a := tr.doPost(context.Background(), []byte("payload"))
	require.Error(t, a.Err)
	assert.Equal(t, 503, a.Status)
	assert.Equal(t, 2*time.Second, a.RetryAfter) // header parsed and threaded into the Attempt
}

func TestOTLPRetriable(t *testing.T) {
	ctx := context.Background()
	// Per the OTLP/HTTP spec only 429, 502, 503, 504 (and network errors) retry.
	for status, want := range map[int]bool{
		0:   true, // network-level error
		429: true,
		502: true,
		503: true,
		504: true,
		408: false, // retryable under the generic classifier, but not OTLP
		500: false,
		501: false,
		505: false,
		400: false,
		404: false,
		200: false,
	} {
		assert.Equalf(t, want, otlpRetriable(ctx, status), "status %d", status)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	assert.False(t, otlpRetriable(cancelled, 503)) // a cancelled context is never retriable
}

func TestParseRetryAfter(t *testing.T) {
	mk := func(v string) http.Header { return http.Header{"Retry-After": []string{v}} }

	assert.Equal(t, time.Duration(0), parseRetryAfter(http.Header{})) // absent
	assert.Equal(t, time.Duration(0), parseRetryAfter(mk("")))        // empty
	assert.Equal(t, 5*time.Second, parseRetryAfter(mk("5")))          // delta-seconds
	assert.Equal(t, 10*time.Second, parseRetryAfter(mk("  10  ")))    // trimmed
	assert.Equal(t, time.Duration(0), parseRetryAfter(mk("-3")))      // negative clamps to 0
	assert.Equal(t, maxRetryAfter, parseRetryAfter(mk("100000")))     // clamped to the cap
	assert.Equal(t, maxRetryAfter, parseRetryAfter(mk("9223372037"))) // overflow value clamped, not wrapped negative
	assert.Equal(t, time.Duration(0), parseRetryAfter(mk("soon")))    // unparseable
	assert.Equal(t, time.Duration(0), parseRetryAfter(mk("0")))       // zero -> fall back to backoff

	// HTTP-date in the future yields a positive, capped delay.
	future := time.Now().Add(3 * time.Second).UTC().Format(http.TimeFormat)
	d := parseRetryAfter(mk(future))
	assert.Greater(t, d, time.Duration(0))
	assert.LessOrEqual(t, d, maxRetryAfter)

	// HTTP-date in the past clamps to 0.
	past := time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)
	assert.Equal(t, time.Duration(0), parseRetryAfter(mk(past)))
}

func TestOTLPStatusMessage(t *testing.T) {
	// A google.rpc.Status: field 1 = code (varint), field 2 = message (string).
	var body []byte
	body = protowire.AppendTag(body, 1, protowire.VarintType)
	body = protowire.AppendVarint(body, 3) // INVALID_ARGUMENT
	body = protowire.AppendTag(body, 2, protowire.BytesType)
	body = protowire.AppendBytes(body, []byte("invalid trace_id length"))
	assert.Equal(t, "invalid trace_id length", otlpStatusMessage(body))

	// Missing message field -> "".
	var codeOnly []byte
	codeOnly = protowire.AppendTag(codeOnly, 1, protowire.VarintType)
	codeOnly = protowire.AppendVarint(codeOnly, 5)
	assert.Equal(t, "", otlpStatusMessage(codeOnly))

	// Non-protobuf garbage -> "" (best-effort, never panics).
	assert.Equal(t, "", otlpStatusMessage([]byte{0xff, 0xff, 0xff}))
	assert.Equal(t, "", otlpStatusMessage(nil))
}

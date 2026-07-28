// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package transport

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestExportPostDrainsBodyForConnReuse locks the drain after ExportPost's capped
// read: without it a response larger than the 1MiB cap leaves the connection
// unread, so net/http cannot reuse the keep-alive connection for the next chunk.
// Two sequential ExportPost calls against a keep-alive server must therefore open
// exactly one TCP connection.
func TestExportPostDrainsBodyForConnReuse(t *testing.T) {
	// 1.5MiB: larger than ExportPost's 1MiB read cap, but within reach of the
	// follow-up 1MiB drain, so the body still reaches EOF and the connection
	// stays reusable. Without the drain the second call dials a new connection.
	respBody := bytes.Repeat([]byte("x"), (1<<20)+(1<<19))

	var newConns atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(respBody)
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			newConns.Add(1)
		}
	}
	srv.Start()
	defer srv.Close()

	tr := &Transport{
		httpClient:  srv.Client(),
		testBaseURL: srv.URL,
		agentless:   true,
	}

	ctx := context.Background()
	for range 2 {
		res, err := tr.ExportPost(ctx, EndpointLLMSpan, SubdomainLLMSpan, "application/json", []byte("{}"))
		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, res.StatusCode)
		assert.Len(t, res.Body, 1<<20) // response truncated to the 1MiB read cap
	}

	assert.Equal(t, int32(1), newConns.Load(),
		"expected the keep-alive connection to be reused across ExportPost calls")
}

func TestHasRetryAfterHeader(t *testing.T) {
	assert.False(t, hasRetryAfterHeader(http.Header{}))
	assert.True(t, hasRetryAfterHeader(http.Header{"Retry-After": []string{"5"}}))
	assert.True(t, hasRetryAfterHeader(http.Header{"X-Ratelimit-Reset": []string{"5"}}))
}

func mkHeader(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(kv); i += 2 {
		h.Set(kv[i], kv[i+1])
	}
	return h
}

// TestParseRetryAfterIgnoresRetryAfterHeader pins the live 429 path's timing. The
// live flush passes context.Background() on the paths tracer.Stop() waits for, so
// honoring a server-advertised Retry-After here would let one 429 stall shutdown
// for as long as the server asks. Only parseExportRetryAfter reads that header.
func TestParseRetryAfterIgnoresRetryAfterHeader(t *testing.T) {
	cases := []struct {
		name string
		h    http.Header
		want time.Duration
	}{
		{"Retry-After is ignored", mkHeader("Retry-After", "890"), time.Second},
		{"x-ratelimit-reset still honored", mkHeader("x-ratelimit-reset", "4"), 4 * time.Second},
		{"Retry-After does not override x-ratelimit-reset", mkHeader("Retry-After", "890", "x-ratelimit-reset", "3"), 3 * time.Second},
		{"default 1s when no header", http.Header{}, time.Second},
	}
	for _, tc := range cases {
		if got := parseRetryAfter(tc.h); got != tc.want {
			t.Errorf("%s: parseRetryAfter = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestParseExportRetryAfter(t *testing.T) {
	cases := []struct {
		name string
		h    http.Header
		want time.Duration
	}{
		{"standard Retry-After (delta-seconds)", mkHeader("Retry-After", "5"), 5 * time.Second},
		{"Retry-After wins over x-ratelimit-reset", mkHeader("Retry-After", "7", "x-ratelimit-reset", "3"), 7 * time.Second},
		{"x-ratelimit-reset fallback (duration seconds)", mkHeader("x-ratelimit-reset", "4"), 4 * time.Second},
		{"default 1s when no header", http.Header{}, time.Second},
		{"non-positive Retry-After falls back to default", mkHeader("Retry-After", "0"), time.Second},
		{"unparseable Retry-After falls back to default", mkHeader("Retry-After", "soon"), time.Second},
		// Both header forms are clamped, so no single response can park an export
		// for minutes inside one Submit call.
		{"long Retry-After is clamped", mkHeader("Retry-After", "890"), maxExportRetryAfter},
		{"long x-ratelimit-reset is clamped", mkHeader("x-ratelimit-reset", "890"), maxExportRetryAfter},
	}
	for _, tc := range cases {
		if got := parseExportRetryAfter(tc.h); got != tc.want {
			t.Errorf("%s: parseExportRetryAfter = %v, want %v", tc.name, got, tc.want)
		}
	}

	// An HTTP-date in the future yields a positive, roughly-correct delay.
	future := time.Now().Add(10 * time.Second).UTC().Format(http.TimeFormat)
	if got := parseExportRetryAfter(mkHeader("Retry-After", future)); got < minExportRetryAfter || got > 11*time.Second {
		t.Errorf("HTTP-date Retry-After: got %v, want in [%v, 11s]", got, minExportRetryAfter)
	}
	// A far-future HTTP-date is clamped too; time.Until would otherwise be honored
	// uncapped.
	far := time.Now().Add(24 * time.Hour).UTC().Format(http.TimeFormat)
	if got := parseExportRetryAfter(mkHeader("Retry-After", far)); got != maxExportRetryAfter {
		t.Errorf("far-future HTTP-date Retry-After: got %v, want %v", got, maxExportRetryAfter)
	}

	// The floor matters because backoff.RetryAfter takes WHOLE seconds. HTTP dates
	// have 1-second granularity, so a date ~1s out leaves a sub-second remainder
	// that would truncate to 0 — which backoff/v5 treats as "retry now" AND resets
	// the exponential backoff, hammering the very intake that asked us to wait.
	soon := time.Now().Add(400 * time.Millisecond).UTC().Format(http.TimeFormat)
	got := parseExportRetryAfter(mkHeader("Retry-After", soon))
	if got < minExportRetryAfter {
		t.Errorf("near-future HTTP-date Retry-After: got %v, want >= %v", got, minExportRetryAfter)
	}
	if int(got.Seconds()) == 0 {
		t.Errorf("delay %v truncates to 0 whole seconds, defeating the honored Retry-After", got)
	}
}

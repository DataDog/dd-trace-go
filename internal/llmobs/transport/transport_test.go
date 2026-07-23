// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

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

// TestExportPostDrainsBodyForConnReuse guards the fix for the review finding that
// ExportPost closed the response body without draining it. Because ExportPost caps
// the read at 1MiB, a larger body left the connection unread, so net/http could not
// reuse the keep-alive connection for the next chunk. Two sequential ExportPost calls
// against a keep-alive server must therefore open exactly one TCP connection.
func TestExportPostDrainsBodyForConnReuse(t *testing.T) {
	// 1.5MiB: larger than ExportPost's 1MiB read cap, but within reach of the
	// follow-up 1MiB drain, so the body still reaches EOF and the connection
	// stays reusable. Without the drain the second call dials a new connection.
	respBody := bytes.Repeat([]byte("x"), (1<<20)+(1<<19))

	var newConns int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(respBody)
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			atomic.AddInt32(&newConns, 1)
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

	assert.Equal(t, int32(1), atomic.LoadInt32(&newConns),
		"expected the keep-alive connection to be reused across ExportPost calls")
}

func TestHasRetryAfterHeader(t *testing.T) {
	assert.False(t, hasRetryAfterHeader(http.Header{}))
	assert.True(t, hasRetryAfterHeader(http.Header{"Retry-After": []string{"5"}}))
	assert.True(t, hasRetryAfterHeader(http.Header{"X-Ratelimit-Reset": []string{"5"}}))
}

func TestParseRetryAfter(t *testing.T) {
	mk := func(kv ...string) http.Header {
		h := http.Header{}
		for i := 0; i+1 < len(kv); i += 2 {
			h.Set(kv[i], kv[i+1])
		}
		return h
	}
	cases := []struct {
		name string
		h    http.Header
		want time.Duration
	}{
		{"standard Retry-After (delta-seconds)", mk("Retry-After", "5"), 5 * time.Second},
		{"Retry-After wins over x-ratelimit-reset", mk("Retry-After", "7", "x-ratelimit-reset", "3"), 7 * time.Second},
		{"x-ratelimit-reset fallback (duration seconds)", mk("x-ratelimit-reset", "4"), 4 * time.Second},
		{"default 1s when no header", http.Header{}, time.Second},
		{"non-positive Retry-After falls back to default", mk("Retry-After", "0"), time.Second},
		{"unparseable Retry-After falls back to default", mk("Retry-After", "soon"), time.Second},
	}
	for _, tc := range cases {
		if got := parseRetryAfter(tc.h); got != tc.want {
			t.Errorf("%s: parseRetryAfter = %v, want %v", tc.name, got, tc.want)
		}
	}

	// An HTTP-date in the future yields a positive, roughly-correct delay.
	future := time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(mk("Retry-After", future)); got <= 0 || got > 31*time.Second {
		t.Errorf("HTTP-date Retry-After: got %v, want in (0, 31s]", got)
	}
}

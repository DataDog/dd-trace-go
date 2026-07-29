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

func TestPushSpanEventsWithResult(t *testing.T) {
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
		res, err := tr.PushSpanEventsWithResult(ctx, []*LLMObsSpanEvent{{
			TraceID: "trace",
			SpanID:  "span",
		}})
		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, res.StatusCode)
		assert.Equal(t, respBody, res.Body)
	}

	assert.Equal(t, int32(1), newConns.Load(),
		"expected the keep-alive connection to be reused across requests")
}

func mkHeader(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(kv); i += 2 {
		h.Set(kv[i], kv[i+1])
	}
	return h
}

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

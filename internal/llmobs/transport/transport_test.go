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

func TestRequestDrainsLargeErrorResponse(t *testing.T) {
	respBody := bytes.Repeat([]byte("x"), 3<<20)
	var newConns atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
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
	for range 2 {
		res, err := tr.PushSpanEventsWithResult(context.Background(), []*LLMObsSpanEvent{{
			TraceID: "trace",
			SpanID:  "span",
		}})
		assert.Error(t, err)
		assert.Len(t, res.Body, 1<<20)
	}
	assert.Equal(t, int32(1), newConns.Load())
}

func mkHeader(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(kv); i += 2 {
		h.Set(kv[i], kv[i+1])
	}
	return h
}

func TestParseRetryAfter(t *testing.T) {
	cases := []struct {
		name string
		h    http.Header
		want time.Duration
	}{
		{name: "ignores Retry-After", h: mkHeader("Retry-After", "890"), want: time.Second},
		{name: "uses x-ratelimit-reset", h: mkHeader("x-ratelimit-reset", "4"), want: 4 * time.Second},
		{name: "x-ratelimit-reset takes precedence", h: mkHeader("Retry-After", "890", "x-ratelimit-reset", "3"), want: 3 * time.Second},
		{name: "defaults to one second", h: http.Header{}, want: time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, parseRetryAfter(tc.h))
		})
	}
}

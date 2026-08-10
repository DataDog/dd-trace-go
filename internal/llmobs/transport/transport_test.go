// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/config"
)

// sizedServer answers every request with statusCode and a JSON body of
// totalBytes, written in small chunks. It records how much it managed to write
// and how many requests it received.
type sizedServer struct {
	*httptest.Server
	written  atomic.Int64
	requests atomic.Int64
}

func newSizedServer(t *testing.T, statusCode int, totalBytes int64) *sizedServer {
	t.Helper()
	const chunkSize = 4 << 10
	s := &sizedServer{}
	chunk := bytes.Repeat([]byte("a"), chunkSize)
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		for remaining := totalBytes; remaining > 0; {
			n, err := w.Write(chunk[:min(remaining, chunkSize)])
			s.written.Add(int64(n))
			if err != nil {
				return
			}
			remaining -= int64(n)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func newTestTransport(t *testing.T, baseURL string) *Transport {
	t.Helper()
	agentURL, err := url.Parse("http://127.0.0.1:8126")
	require.NoError(t, err)
	return New(&config.Config{
		TracerConfig: config.TracerConfig{
			AgentURL:   agentURL,
			HTTPClient: &http.Client{},
		},
		TestBaseURL: baseURL,
	})
}

// TestResponseBodySizeLimit checks that the transport stops buffering a response
// body once it grows past the limit the request declared, rather than holding
// however much the server decides to send.
func TestResponseBodySizeLimit(t *testing.T) {
	const limit = 16 << 10
	lim := requestLimits{timeout: 5 * time.Second, maxResponseSize: limit}

	t.Run("body within limit is returned in full", func(t *testing.T) {
		srv := newSizedServer(t, http.StatusOK, limit)
		c := newTestTransport(t, srv.URL)

		res, err := c.jsonRequest(context.Background(), http.MethodPost, endpointLLMSpan, subdomainLLMSpan, nil, lim)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, res.statusCode)
		assert.Len(t, res.body, limit)
	})

	t.Run("body over limit is rejected", func(t *testing.T) {
		const total = 8 << 20
		srv := newSizedServer(t, http.StatusOK, total)
		c := newTestTransport(t, srv.URL)

		res, err := c.jsonRequest(context.Background(), http.MethodPost, endpointLLMSpan, subdomainLLMSpan, nil, lim)
		require.ErrorIs(t, err, errResponseTooLarge)
		assert.Empty(t, res.body)
		assert.Less(t, srv.written.Load(), int64(total),
			"server should have been cut off before sending the whole body")
	})

	t.Run("oversized body is not retried", func(t *testing.T) {
		srv := newSizedServer(t, http.StatusOK, 8<<20)
		c := newTestTransport(t, srv.URL)

		_, err := c.jsonRequest(context.Background(), http.MethodPost, endpointLLMSpan, subdomainLLMSpan, nil, lim)
		require.ErrorIs(t, err, errResponseTooLarge)
		assert.Equal(t, int64(1), srv.requests.Load(),
			"retrying would buffer the same oversized body again")
	})

	t.Run("error message from a failed request is bounded", func(t *testing.T) {
		srv := newSizedServer(t, http.StatusBadRequest, 8<<20)
		c := newTestTransport(t, srv.URL)

		_, err := c.jsonRequest(context.Background(), http.MethodPost, endpointLLMSpan, subdomainLLMSpan, nil, lim)
		require.Error(t, err)
		assert.Less(t, len(err.Error()), 2*errorBodySize,
			"only a bounded prefix of the response should reach the error message")
	})

	// Endpoints that echo back a payload the caller sent need room for it, so
	// they must not be held to the acknowledgement-sized limit.
	t.Run("experiment creation accepts its echoed config", func(t *testing.T) {
		cfg := map[string]any{"prompt": strings.Repeat("a", 2*ackResponseSize)}
		body, err := json.Marshal(CreateExperimentResponse{
			Data: ResponseData[ExperimentView]{
				ID:         "exp-1",
				Attributes: ExperimentView{Name: "exp-name", Config: cfg},
			},
		})
		require.NoError(t, err)
		require.Greater(t, len(body), ackResponseSize)

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write(body)
		}))
		t.Cleanup(srv.Close)
		c := newTestTransport(t, srv.URL)

		exp, err := c.CreateExperiment(context.Background(), "exp-name", "ds-1", "proj-1", 1, cfg, nil, "", 1)
		require.NoError(t, err)
		assert.Equal(t, "exp-1", exp.ID)
	})

	// The subtests above inject their own limit; this one goes through a real
	// endpoint to check the limit it declares is applied.
	t.Run("span upload declares a limit", func(t *testing.T) {
		const total = 8 * ackResponseSize
		srv := newSizedServer(t, http.StatusOK, total)
		c := newTestTransport(t, srv.URL)

		err := c.PushSpanEvents(context.Background(), []*LLMObsSpanEvent{{Name: "test-span"}})
		require.ErrorIs(t, err, errResponseTooLarge)
		assert.Less(t, srv.written.Load(), int64(total),
			"server should have been cut off before sending the whole body")
	})
}

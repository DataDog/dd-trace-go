// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/config"
)

type unencodableValue struct{}

func (unencodableValue) MarshalJSON() ([]byte, error) {
	return nil, errors.New("cannot encode value")
}

type readErrorBody struct{}

func (readErrorBody) Read(p []byte) (int, error) {
	return copy(p, "partial"), io.ErrUnexpectedEOF
}

func (readErrorBody) Close() error { return nil }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestPushSpanEventsWithResult(t *testing.T) {
	respBody := bytes.Repeat([]byte("x"), 1<<10)

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

func TestRequestDrainsErrorResponse(t *testing.T) {
	respBody := bytes.Repeat([]byte("x"), 2*errorBodySize)
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
		require.Error(t, err)
		assert.Len(t, res.Body, errorBodySize)
	}
	assert.Equal(t, int32(1), newConns.Load())
}

func TestRequestRetriesTransientStatus(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if requests.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"retry"}`))
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"accepted":true}`))
	}))
	t.Cleanup(server.Close)

	transport := newTestTransport(t, server.URL)
	result, err := transport.PushSpanEventsBodyWithResult(context.Background(), []byte(`{}`))
	require.NoError(t, err)
	assert.Equal(t, int32(2), requests.Load())
	assert.Equal(t, http.StatusAccepted, result.StatusCode)
	assert.Equal(t, 2, result.Attempts)
	assert.Equal(t, []byte(`{"accepted":true}`), result.Body)
	assert.False(t, result.Retriable)
}

func TestRequestReportsExhaustedRetries(t *testing.T) {
	response := []byte(`{"error":"retry"}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write(response)
	}))
	t.Cleanup(server.Close)

	transport := newTestTransport(t, server.URL)
	result, err := transport.PushSpanEventsBodyWithResult(context.Background(), []byte(`{}`))
	require.ErrorContains(t, err, "transient http status code: 500")
	assert.Equal(t, http.StatusInternalServerError, result.StatusCode)
	assert.Equal(t, int(defaultMaxRetries), result.Attempts)
	assert.Equal(t, response, result.Body)
	assert.True(t, result.Retriable)
}

func TestRequestReportsSuccessfulStatusWhenResponseReadFails(t *testing.T) {
	var requests atomic.Int32
	transport := &Transport{
		httpClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests.Add(1)
			return &http.Response{
				StatusCode: http.StatusAccepted,
				Header:     make(http.Header),
				Body:       readErrorBody{},
			}, nil
		})},
		testBaseURL: "http://llmobs.test",
		agentless:   true,
	}

	result, err := transport.PushSpanEventsBodyWithResult(context.Background(), []byte(`{}`))
	require.ErrorContains(t, err, "failed to read response body")
	assert.Equal(t, int32(defaultMaxRetries), requests.Load())
	assert.Equal(t, http.StatusAccepted, result.StatusCode)
	assert.Equal(t, int(defaultMaxRetries), result.Attempts)
	assert.Equal(t, []byte("partial"), result.Body)
	assert.True(t, result.Retriable)
}

func TestNewRequest(t *testing.T) {
	transport := &Transport{
		defaultHeaders: map[string]string{"DD-API-KEY": "key"},
	}
	request, err := transport.newRequest(
		context.Background(), http.MethodPost, "http://localhost/path", "subdomain", "application/json", bytes.NewReader(nil),
	)
	require.NoError(t, err)
	assert.Equal(t, "application/json", request.Header.Get("Content-Type"))
	assert.Equal(t, "key", request.Header.Get("DD-API-KEY"))
	assert.Equal(t, "subdomain", request.Header.Get(headerEVPSubdomain))

	transport.agentless = true
	request, err = transport.newRequest(
		context.Background(), http.MethodPost, "http://localhost/path", "subdomain", "application/json", nil,
	)
	require.NoError(t, err)
	assert.Empty(t, request.Header.Get(headerEVPSubdomain))

	_, err = transport.newRequest(
		context.Background(), "invalid method", "http://localhost/path", "subdomain", "application/json", nil,
	)
	require.Error(t, err)
}

func TestResponseHelpers(t *testing.T) {
	assert.Empty(t, errorBodyMessage(mkHeader("Content-Type", "text/plain"), []byte("ignored")))
	assert.Equal(t, "message", errorBodyMessage(mkHeader("Content-Type", "application/json"), []byte(" message\n")))
	assert.Nil(t, readResponseBody(nil))
	drainAndClose(nil)
}

func TestIsRetriableStatus(t *testing.T) {
	for _, status := range []int{http.StatusRequestTimeout, http.StatusTooEarly, http.StatusInternalServerError, 599} {
		assert.True(t, isRetriableStatus(status), status)
	}
	for _, status := range []int{http.StatusBadRequest, http.StatusTooManyRequests, 600} {
		assert.False(t, isRetriableStatus(status), status)
	}
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

	future := strconv.FormatInt(time.Now().Add(10*time.Second).Unix(), 10)
	wait := parseRetryAfter(mkHeader(headerRateLimitReset, future))
	assert.Positive(t, wait)
	assert.LessOrEqual(t, wait, 10*time.Second)
}

func TestMarshalJSON(t *testing.T) {
	body, err := MarshalJSON(map[string]string{"html": "<value>"})
	require.NoError(t, err)
	assert.Equal(t, `{"html":"<value>"}`, string(body))

	_, err = MarshalJSON(unencodableValue{})
	require.ErrorContains(t, err, "cannot encode value")
}

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

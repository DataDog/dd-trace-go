// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package gorilla_mux

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/net"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type TestCaseSubrouter struct {
	*http.Server
}

func (tc *TestCaseSubrouter) Setup(_ context.Context, t *testing.T) {
	router := mux.NewRouter()
	sub := router.PathPrefix("/sub").Subrouter() // type: *mux.Router
	sub.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	router = sub

	tc.Server = &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", net.FreePort(t)),
		Handler: router,
	}

	go func() { assert.ErrorIs(t, tc.Server.ListenAndServe(), http.ErrServerClosed) }()
	t.Cleanup(func() {
		// Using a new 10s-timeout context, as we may be running cleanup after the original context expired.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		assert.NoError(t, tc.Server.Shutdown(ctx))
	})
}

func (tc *TestCaseSubrouter) Run(_ context.Context, t *testing.T) {
	resp, err := http.Get(fmt.Sprintf("http://%s/sub/ping", tc.Server.Addr))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func (tc *TestCaseSubrouter) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name":     "http.request",
				"resource": "GET /sub/ping",
				"type":     "http",
				"service":  "gorilla_mux.test",
			},
		},
	}
}

type TestCaseRouterParallel struct {
	*http.Server
}

func (tc *TestCaseRouterParallel) Setup(_ context.Context, t *testing.T) {
	router := mux.NewRouter()
	router.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	tc.Server = &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", net.FreePort(t)),
		Handler: router,
	}

	go func() { assert.ErrorIs(t, tc.Server.ListenAndServe(), http.ErrServerClosed) }()
	t.Cleanup(func() {
		// Using a new 10s-timeout context, as we may be running cleanup after the original context expired.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		assert.NoError(t, tc.Server.Shutdown(ctx))
	})
}

func (tc *TestCaseRouterParallel) Run(_ context.Context, t *testing.T) {
	// Test sync.Once behavior by making concurrent requests to the router
	// This ensures initialization happens exactly once even with parallel invocations
	const numRequests = 10
	var wg sync.WaitGroup
	wg.Add(numRequests)

	errChan := make(chan error, numRequests)
	statusChan := make(chan int, numRequests)

	for i := 0; i < numRequests; i++ {
		go func() {
			defer wg.Done()
			resp, err := http.Get(fmt.Sprintf("http://%s/ping", tc.Server.Addr))
			if err != nil {
				errChan <- err
				return
			}
			defer resp.Body.Close()
			statusChan <- resp.StatusCode
		}()
	}

	wg.Wait()
	close(errChan)
	close(statusChan)

	// Verify all requests succeeded
	for err := range errChan {
		require.NoError(t, err)
	}

	successCount := 0
	for status := range statusChan {
		require.Equal(t, http.StatusOK, status, "Expected all parallel requests to succeed")
		successCount++
	}
	require.Equal(t, numRequests, successCount, "Expected all %d requests to complete", numRequests)
}

func (tc *TestCaseRouterParallel) ExpectedTraces() trace.Traces {
	// We expect exactly numRequests traces, all for the same endpoint
	// Each concurrent request should produce its own trace
	const numRequests = 10
	traces := make(trace.Traces, numRequests)
	for i := 0; i < numRequests; i++ {
		traces[i] = &trace.Trace{
			Tags: map[string]any{
				"name":     "http.request",
				"resource": "GET /ping",
				"type":     "http",
				"service":  "gorilla_mux.test",
			},
		}
	}
	return traces
}

type TestCase struct {
	*http.Server
}

func (tc *TestCase) Setup(_ context.Context, t *testing.T) {
	router := mux.NewRouter()
	tc.Server = &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", net.FreePort(t)),
		Handler: router,
	}
	router.HandleFunc("/ping", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, err := io.WriteString(w, `{"message": "pong"}`)
		assert.NoError(t, err)
	}).Methods("GET")

	go func() { assert.ErrorIs(t, tc.Server.ListenAndServe(), http.ErrServerClosed) }()
	t.Cleanup(func() {
		// Using a new 10s-timeout context, as we may be running cleanup after the original context expired.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		assert.NoError(t, tc.Server.Shutdown(ctx))
	})
}

func (tc *TestCase) Run(_ context.Context, t *testing.T) {
	resp, err := http.Get(fmt.Sprintf("http://%s/ping", tc.Server.Addr))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func (tc *TestCase) ExpectedTraces() trace.Traces {
	url := fmt.Sprintf("http://%s/ping", tc.Server.Addr)
	return trace.Traces{
		{
			// NB: 2 Top-level spans are from the HTTP Client/Server, which are library-side instrumented.
			Tags: map[string]any{
				"name":     "http.request",
				"resource": "GET /ping",
				"type":     "http",
				"service":  "gorilla_mux.test",
			},
			Meta: map[string]string{
				"http.url":  url,
				"component": "net/http",
				"span.kind": "client",
			},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":     "http.request",
						"resource": "GET /ping",
						"type":     "web",
						"service":  "http.router",
					},
					Meta: map[string]string{
						"http.url":  url,
						"component": "net/http",
						"span.kind": "server",
					},
					Children: trace.Traces{
						{
							Tags: map[string]any{
								"name":     "http.request",
								"resource": "GET /ping",
								"type":     "web",
								"service":  "mux.router",
							},
							Meta: map[string]string{
								"http.url":  url,
								"component": "gorilla/mux",
								"span.kind": "server",
							},
							Children: nil,
						},
					},
				},
			},
		},
	}
}

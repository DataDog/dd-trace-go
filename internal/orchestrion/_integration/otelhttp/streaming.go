// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelhttp tests a streaming response through an otelhttp client while the
// OpenTelemetry API bridge is on. The otelhttp client span ends only after the response
// body is read to EOF or closed. While it stays open, it sits on the goroutine-local
// stack, so a later span that starts with context.Background() on the same goroutine
// takes it as parent. This is the moby bug class: the Docker client streams responses
// that it does not always read to the end.
package otelhttp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/DataDog/orchestrion/runtime/built"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/net"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCase checks that the otelhttp client span of a streaming response ends when the
// body is read to EOF, so a later span with context.Background() on the same goroutine
// stays a root and forms a separate trace.
type TestCase struct {
	srv *http.Server
}

// PreBootstrap turns on the OpenTelemetry API bridge. The bridge does nothing unless
// DD_TRACE_OTEL_ENABLED is true.
func (*TestCase) PreBootstrap(_ context.Context, t *testing.T) {
	t.Setenv("DD_TRACE_OTEL_ENABLED", "true")
}

func (tc *TestCase) Setup(_ context.Context, t *testing.T) {
	if !built.WithOrchestrion {
		t.Skip("orchestrion weaves the OpenTelemetry API bridge")
	}

	ln := net.FreeListener(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/stream", tc.handleStream)
	tc.srv = &http.Server{
		Addr:         ln.Addr().String(),
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() { assert.ErrorIs(t, tc.srv.Serve(ln), http.ErrServerClosed) }()
	t.Cleanup(func() {
		// Use a new context, because the original context may have expired during cleanup.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		assert.NoError(t, tc.srv.Shutdown(ctx))
	})
}

// handleStream streams several chunks, with a flush after each one. The handler returns
// only after every chunk is written, so the response completes while the client reads.
func (tc *TestCase) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	for i := range 3 {
		fmt.Fprintf(w, "chunk %d\n", i)
		flusher.Flush()
	}
}

func (tc *TestCase) Run(_ context.Context, t *testing.T) {
	client := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		fmt.Sprintf("http://%s/stream", tc.srv.Addr), nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// The context of the request is dropped, as moby does. Reading the body to EOF on
	// this goroutine ends the otelhttp client span.
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.NotEmpty(t, body)

	// A later span on the same goroutine, started with context.Background(). If the
	// otelhttp client span were still on the goroutine-local stack, it would be the
	// parent here.
	later, _ := tracer.StartSpanFromContext(context.Background(), "later")
	defer later.Finish()

	require.Zero(t, parentID(later), "the later span must be a root after the streamed body ended")
}

func (tc *TestCase) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name":     "http.client.request",
				"resource": "HTTP GET",
			},
			Meta: map[string]string{
				"span.kind": "client",
			},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":     "http.request",
						"resource": "GET /stream",
						"type":     "http",
					},
					Meta: map[string]string{
						"component": "net/http",
						"span.kind": "client",
					},
					Children: trace.Traces{
						{
							Tags: map[string]any{
								"name":     "http.request",
								"resource": "GET /stream",
								"type":     "web",
							},
							Meta: map[string]string{
								"component": "net/http",
								"span.kind": "server",
							},
						},
					},
				},
			},
		},
		{
			Tags: map[string]any{
				"name":     "later",
				"resource": "later",
			},
		},
	}
}

func parentID(s *tracer.Span) uint64 {
	return s.AsMap()[ext.MapSpanParentID].(uint64)
}

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package echo

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/net"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type TestCase struct {
	*echo.Echo
	addr string
}

func (*TestCase) PreBootstrap(_ context.Context, t *testing.T) {
	t.Setenv("DD_TRACE_ECHO_IGNORED_ROUTES", "GET /health")
}

func (tc *TestCase) Setup(_ context.Context, t *testing.T) {
	tc.Echo = echo.New()
	tc.Echo.Logger.SetOutput(io.Discard)

	tc.Echo.GET("/health", func(c echo.Context) error {
		return c.NoContent(http.StatusNoContent)
	})
	tc.Echo.GET("/ping", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]any{"message": "pong"})
	})
	ln := net.FreeListener(t)
	tc.addr = ln.Addr().String()
	tc.Echo.Listener = ln

	go func() { assert.ErrorIs(t, tc.Echo.Start(""), http.ErrServerClosed) }()
	t.Cleanup(func() {
		// Using a new 10s-timeout context, as we may be running cleanup after the original context expired.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		require.NoError(t, tc.Echo.Shutdown(ctx))
	})
}

func (tc *TestCase) Run(_ context.Context, t *testing.T) {
	tc.request(t, "/health", http.StatusNoContent)
	tc.request(t, "/ping", http.StatusOK)
}

func (tc *TestCase) request(t *testing.T, path string, wantStatus int) {
	t.Helper()
	resp, err := http.Get("http://" + tc.addr + path)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, wantStatus, resp.StatusCode)
}

func (*TestCase) ExpectedSpanCount() int {
	// The ignored /health route produces only HTTP client/server spans. The traced
	// /ping route additionally produces the Echo middleware span.
	return 5
}

func (tc *TestCase) ExpectedTraces() trace.Traces {
	return trace.Traces{
		tc.expectedTrace("/health", nil),
		tc.expectedTrace("/ping", trace.Traces{
			{
				Tags: map[string]any{
					"name":     "http.request",
					"service":  "echo",
					"resource": "GET /ping",
					"type":     "web",
				},
				Meta: map[string]string{
					"http.url":  "http://" + tc.addr + "/ping",
					"component": "labstack/echo.v4",
					"span.kind": "server",
				},
			},
		}),
	}
}

func (tc *TestCase) expectedTrace(path string, children trace.Traces) *trace.Trace {
	httpURL := "http://" + tc.addr + path
	resource := "GET " + path
	return &trace.Trace{
		// NB: 2 Top-level spans are from the HTTP Client/Server, which are library-side instrumented.
		Tags: map[string]any{
			"name":     "http.request",
			"resource": resource,
			"service":  "echo.v4.test",
			"type":     "http",
		},
		Meta: map[string]string{
			"http.url":  httpURL,
			"component": "net/http",
			"span.kind": "client",
		},
		Children: trace.Traces{
			{
				Tags: map[string]any{
					"name":     "http.request",
					"resource": resource,
					"service":  "http.router",
					"type":     "web",
				},
				Meta: map[string]string{
					"http.url":  httpURL,
					"component": "net/http",
					"span.kind": "server",
				},
				Children: children,
			},
		},
	}
}

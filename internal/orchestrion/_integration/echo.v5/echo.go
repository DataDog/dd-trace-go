// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package echo

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type TestCase struct {
	*echo.Echo
	srv *httptest.Server
}

func (tc *TestCase) Setup(_ context.Context, t *testing.T) {
	tc.Echo = echo.New()
	tc.Echo.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))

	tc.Echo.GET("/ping", func(c *echo.Context) error {
		return c.JSON(http.StatusOK, map[string]any{"message": "pong"})
	})

	// Echo v5 dropped Echo.Shutdown in favor of context-cancelled
	// StartConfig.Start; using httptest.NewServer is the simplest way to
	// stand up the Echo handler with deterministic teardown for tests.
	tc.srv = httptest.NewServer(tc.Echo)
	t.Cleanup(tc.srv.Close)
}

func (tc *TestCase) Run(_ context.Context, t *testing.T) {
	resp, err := http.Get(tc.srv.URL + "/ping")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func (tc *TestCase) ExpectedTraces() trace.Traces {
	httpURL := tc.srv.URL + "/ping"
	return trace.Traces{
		{
			// NB: 2 Top-level spans are from the HTTP Client/Server, which are library-side instrumented.
			Tags: map[string]any{
				"name":     "http.request",
				"resource": "GET /ping",
				"service":  "echo.v5.test",
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
						"resource": "GET /ping",
						"service":  "http.router",
						"type":     "web",
					},
					Meta: map[string]string{
						"http.url":  httpURL,
						"component": "net/http",
						"span.kind": "server",
					},
					Children: trace.Traces{
						{
							Tags: map[string]any{
								"name":     "http.request",
								"service":  "echo",
								"resource": "GET /ping",
								"type":     "web",
							},
							Meta: map[string]string{
								"http.url":  httpURL,
								"component": "labstack/echo.v5",
								"span.kind": "server",
							},
						},
					},
				},
			},
		},
	}
}

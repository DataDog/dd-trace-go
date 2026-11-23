// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

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

// TestCaseNewWithConfig checks that an instance created with
// echo.NewWithConfig is traced once. NewWithConfig calls echo.New inside echo,
// which must not add a second Echo span.
type TestCaseNewWithConfig struct {
	*echo.Echo
	srv *httptest.Server
}

func (tc *TestCaseNewWithConfig) Setup(_ context.Context, t *testing.T) {
	tc.Echo = echo.NewWithConfig(echo.Config{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	tc.Echo.GET("/config", func(c *echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	tc.srv = httptest.NewServer(tc.Echo)
	t.Cleanup(tc.srv.Close)
}

func (tc *TestCaseNewWithConfig) Run(_ context.Context, t *testing.T) {
	resp, err := http.Get(tc.srv.URL + "/config")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func (*TestCaseNewWithConfig) ExpectedSpanCount() int {
	// The HTTP client and server spans, and one Echo middleware span.
	return 3
}

func (tc *TestCaseNewWithConfig) ExpectedTraces() trace.Traces {
	httpURL := tc.srv.URL + "/config"
	return trace.Traces{
		{
			Tags: map[string]any{
				"name":     "http.request",
				"resource": "GET /config",
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
						"resource": "GET /config",
						"type":     "web",
					},
					Meta: map[string]string{
						"component": "net/http",
						"span.kind": "server",
					},
					Children: trace.Traces{
						{
							Tags: map[string]any{
								"name":     "http.request",
								"service":  "echo",
								"resource": "GET /config",
								"type":     "web",
							},
							Meta: map[string]string{
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

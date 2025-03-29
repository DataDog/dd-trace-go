// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package echo

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/internal/orchestrion/_integration/internal/net"
	"github.com/DataDog/dd-trace-go/internal/orchestrion/_integration/internal/trace"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

type TestCase struct {
	*echo.Echo
	addr string
}

func (tc *TestCase) Setup(_ context.Context, t *testing.T) {
	tc.Echo = echo.New()
	tc.Echo.Logger.SetOutput(io.Discard)

	//dd:span
	handlerFunc := func(c echo.Context) error {
		span, _ := tracer.SpanFromContext(c.Request().Context())
		span.SetTag("foo", "bar")
		return c.JSON(http.StatusOK, map[string]any{"message": "pong"})
	}

	tc.Echo.GET("/ping", handlerFunc)
	tc.addr = fmt.Sprintf("127.0.0.1:%d", net.FreePort(t))

	go func() { assert.ErrorIs(t, tc.Echo.Start(tc.addr), http.ErrServerClosed) }()
	t.Cleanup(func() {
		// Using a new 10s-timeout context, as we may be running cleanup after the original context expired.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		require.NoError(t, tc.Echo.Shutdown(ctx))
	})
}

func (tc *TestCase) Run(_ context.Context, t *testing.T) {
	resp, err := http.Get("http://" + tc.addr + "/ping")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func (tc *TestCase) ExpectedTraces() trace.Traces {
	httpUrl := "http://" + tc.addr + "/ping"
	return trace.Traces{
		{
			// NB: 2 Top-level spans are from the HTTP Client/Server, which are library-side instrumented.
			Tags: map[string]any{
				"name":     "http.request",
				"resource": "GET /ping",
				"service":  "echo.v4.test",
				"type":     "http",
			},
			Meta: map[string]string{
				"http.url":  httpUrl,
				"component": "net/http",
				"span.kind": "client",
			},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":     "http.request",
						"resource": "GET /ping",
						"service":  "echo.v4.test",
						"type":     "web",
					},
					Meta: map[string]string{
						"http.url":  httpUrl,
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
								"http.url":  httpUrl,
								"component": "labstack/echo.v4",
								"span.kind": "server",
							},
							Children: trace.Traces{
								{
									Tags: map[string]any{
										"name":     "handlerFunc",
										"service":  "echo",
										"resource": "",
										"type":     "",
									},
									Meta: map[string]string{
										"foo": "bar",
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

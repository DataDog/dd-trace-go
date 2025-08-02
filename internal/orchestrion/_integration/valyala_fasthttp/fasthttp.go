// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package fasthttp

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/net"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

type TestCase struct {
	Addr   string
	Server *fasthttp.Server
}

func fastHTTPHandler(ctx *fasthttp.RequestCtx) {
	switch string(ctx.Path()) {
	case "/ping":
		_, _ = fmt.Fprintf(ctx, "pong")
	default:
		ctx.Error("Not Found", fasthttp.StatusNotFound)
	}
}

func (tc *TestCase) Setup(_ context.Context, t *testing.T) {
	tc.Server = &fasthttp.Server{Handler: fastHTTPHandler}
	tc.Addr = fmt.Sprintf("127.0.0.1:%d", net.FreePort(t))

	go func() { assert.ErrorIs(t, tc.Server.ListenAndServe(tc.Addr), http.ErrServerClosed) }()
	t.Cleanup(func() {
		// Using a new 10s-timeout context, as we may be running cleanup after the original context expired.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		assert.NoError(t, tc.Server.ShutdownWithContext(ctx))
	})
}

func (tc *TestCase) Run(_ context.Context, t *testing.T) {
	resp, err := http.Get("http://" + tc.Addr + "/ping")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func (tc *TestCase) ExpectedTraces() trace.Traces {
	httpUrl := "http://" + tc.Addr + "/ping"
	return trace.Traces{
		{
			// NB: 1 Top-level spans are from the HTTP Client, which are library-side instrumented.
			Tags: map[string]any{
				"name":     "http.request",
				"resource": "GET /ping",
				"service":  "valyala_fasthttp.test",
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
						"service":  "fasthttp",
						"type":     "web",
					},
					Meta: map[string]string{
						"http.url":    httpUrl,
						"http.method": "GET",
						"component":   "valyala/fasthttp",
						"span.kind":   "server",
					},
				},
			},
		},
	}
}

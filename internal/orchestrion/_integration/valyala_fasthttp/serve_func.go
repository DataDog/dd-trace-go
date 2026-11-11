// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package fasthttp

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/net"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseServeFunc checks that a server started with the package-level
// fasthttp.Serve function, not a fasthttp.Server, is traced.
type TestCaseServeFunc struct {
	addr string
}

func (tc *TestCaseServeFunc) Setup(_ context.Context, t *testing.T) {
	ln := net.FreeListener(t)
	tc.addr = ln.Addr().String()
	// fasthttp.Serve returns once the listener is closed.
	go func() { _ = fasthttp.Serve(ln, fastHTTPHandler) }()
	t.Cleanup(func() { _ = ln.Close() })
}

func (tc *TestCaseServeFunc) Run(_ context.Context, t *testing.T) {
	resp, err := http.Get("http://" + tc.addr + "/ping")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// ExpectedSpanCount pins the client span and one fasthttp server span.
func (*TestCaseServeFunc) ExpectedSpanCount() int {
	return 2
}

func (*TestCaseServeFunc) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name":     "http.request",
				"resource": "GET /ping",
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
						"resource": "GET /ping",
						"type":     "web",
					},
					Meta: map[string]string{
						"component": "valyala/fasthttp",
						"span.kind": "server",
					},
				},
			},
		},
	}
}

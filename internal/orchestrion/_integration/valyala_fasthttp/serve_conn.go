// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package fasthttp

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

// TestCaseServeConn serves two connections on one server with
// Server.ServeConn, which does not call Serve.
type TestCaseServeConn struct {
	srv *fasthttp.Server
}

func (tc *TestCaseServeConn) Setup(_ context.Context, t *testing.T) {
	if !otelc.Enabled() {
		t.Skip("orchestrion only instruments Server.Serve")
	}
	tc.srv = &fasthttp.Server{Handler: fastHTTPHandler}
}

func (tc *TestCaseServeConn) Run(_ context.Context, t *testing.T) {
	for range 2 {
		serveConnOnce(t, tc.srv.ServeConn)
	}
}

func (*TestCaseServeConn) ExpectedSpanCount() int { return 2 }

func (*TestCaseServeConn) ExpectedTraces() trace.Traces { return serveConnTraces(2) }

// TestCaseServeConnFunc serves two connections with the package-level
// fasthttp.ServeConn. It reuses pooled servers with a new handler on each
// call, and each call must be traced.
type TestCaseServeConnFunc struct{}

func (*TestCaseServeConnFunc) Setup(_ context.Context, t *testing.T) {
	if !otelc.Enabled() {
		t.Skip("orchestrion only instruments Server.Serve")
	}
}

func (*TestCaseServeConnFunc) Run(_ context.Context, t *testing.T) {
	for range 2 {
		serveConnOnce(t, func(c net.Conn) error { return fasthttp.ServeConn(c, fastHTTPHandler) })
	}
}

func (*TestCaseServeConnFunc) ExpectedSpanCount() int { return 2 }

func (*TestCaseServeConnFunc) ExpectedTraces() trace.Traces { return serveConnTraces(2) }

// TestCaseServeConnGoDefer serves connections with go and defer statements
// that call the package-level fasthttp.ServeConn. Go evaluates the arguments
// at the statement, so changing conn afterwards must not change what is
// served.
type TestCaseServeConnGoDefer struct{}

func (*TestCaseServeConnGoDefer) Setup(_ context.Context, t *testing.T) {
	if !otelc.Enabled() {
		t.Skip("orchestrion only instruments Server.Serve")
	}
}

func (*TestCaseServeConnGoDefer) Run(_ context.Context, t *testing.T) {
	serveConnOnce(t, func(c net.Conn) error {
		conn := c
		go fasthttp.ServeConn(conn, fastHTTPHandler) //nolint:errcheck
		conn = nil
		_ = conn
		return nil
	})
	serveConnOnce(t, func(c net.Conn) error {
		go func() {
			conn := c
			defer fasthttp.ServeConn(conn, fastHTTPHandler) //nolint:errcheck
			conn = nil
			_ = conn
		}()
		return nil
	})
}

func (*TestCaseServeConnGoDefer) ExpectedSpanCount() int { return 2 }

func (*TestCaseServeConnGoDefer) ExpectedTraces() trace.Traces { return serveConnTraces(2) }

// serveConnOnce sends one request over a pipe served by serve.
func serveConnOnce(t *testing.T, serve func(net.Conn) error) {
	t.Helper()
	client, server := net.Pipe()
	served := make(chan error, 1)
	go func() { served <- serve(server) }()

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.SetRequestURI("http://example.com/ping")
	req.SetConnectionClose()
	w := bufio.NewWriter(client)
	require.NoError(t, req.Write(w))
	require.NoError(t, w.Flush())

	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)
	require.NoError(t, resp.Read(bufio.NewReader(client)))
	require.Equal(t, http.StatusOK, resp.StatusCode())
	require.NoError(t, client.Close())
	require.NoError(t, <-served)
}

func serveConnTraces(n int) trace.Traces {
	traces := make(trace.Traces, n)
	for i := range traces {
		traces[i] = &trace.Trace{
			Tags: map[string]any{
				"name":     "http.request",
				"resource": "GET /ping",
				"type":     "web",
			},
			Meta: map[string]string{
				"component": "valyala/fasthttp",
				"span.kind": "server",
			},
		}
	}
	return traces
}

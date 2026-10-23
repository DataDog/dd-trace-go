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

// TestCaseServeConn serves a request on a connection passed to ServeConn,
// which does not call Serve.
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
	client, server := net.Pipe()
	served := make(chan error, 1)
	go func() { served <- tc.srv.ServeConn(server) }()

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

func (*TestCaseServeConn) ExpectedSpanCount() int {
	return 1
}

func (*TestCaseServeConn) ExpectedTraces() trace.Traces {
	return trace.Traces{
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
	}
}

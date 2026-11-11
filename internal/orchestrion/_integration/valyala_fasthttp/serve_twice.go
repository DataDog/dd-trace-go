// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package fasthttp

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/net"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseServeTwice serves one server on two listeners. Each Serve call goes
// through the instrumentation, which must wrap the handler only once.
type TestCaseServeTwice struct {
	addr string
}

func (tc *TestCaseServeTwice) Setup(_ context.Context, t *testing.T) {
	srv := &fasthttp.Server{Handler: fastHTTPHandler}
	first := net.FreeListener(t)
	go func() { _ = srv.Serve(first) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		assert.NoError(t, srv.ShutdownWithContext(ctx))
	})

	// The second Serve call starts only after the first one serves requests,
	// so the first call has already wrapped the handler. The fasthttp client
	// is not traced, so this request adds only a server span.
	require.Eventually(t, func() bool {
		status, _, err := fasthttp.Get(nil, "http://"+first.Addr().String()+"/ping")
		return err == nil && status == http.StatusOK
	}, 10*time.Second, 10*time.Millisecond)

	second := net.FreeListener(t)
	tc.addr = second.Addr().String()
	go func() { _ = srv.Serve(second) }()
}

func (tc *TestCaseServeTwice) Run(_ context.Context, t *testing.T) {
	// The second listener accepts connections as soon as it exists, so the
	// request does not have to wait for its Serve call.
	resp, err := http.Get("http://" + tc.addr + "/ping")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// ExpectedSpanCount pins the server span of the request in Setup, then the
// client span and one server span of the request in Run. A handler wrapped
// twice would add a second server span to the request in Run.
func (*TestCaseServeTwice) ExpectedSpanCount() int {
	return 3
}

func (tc *TestCaseServeTwice) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name":     "http.request",
				"resource": "GET /ping",
				"type":     "http",
			},
			Meta: map[string]string{
				"http.url":  "http://" + tc.addr + "/ping",
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

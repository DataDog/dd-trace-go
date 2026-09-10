// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package nethttp

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseClientGoroutine verifies trace correlation for the net/http client
// shorthands (http.Get, ...) when the call happens on a goroutine that received
// a context.Context as a parameter. This distinguishes call-site context
// injection (which works across goroutines) from GLS-based propagation (which
// does not cross goroutine boundaries): the parent span is started on the test
// goroutine, so it is absent from the spawned goroutine's GLS, and correct
// parenting can only come from the injected context.
type TestCaseClientGoroutine struct {
	base
}

func (tc *TestCaseClientGoroutine) Setup(ctx context.Context, t *testing.T) {
	tc.handler = tc.serveMuxHandler()
	tc.base.Setup(ctx, t)
}

// clientResult carries the outcome of the client call back to the test
// goroutine, so the fatal assertions run there and not on the worker goroutine.
type clientResult struct {
	status int
	err    error
}

func (tc *TestCaseClientGoroutine) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	res := make(chan clientResult, 1)
	go getInGoroutine(ctx, "http://"+tc.srv.Addr+"/hit", res)
	got := <-res
	require.NoError(t, got.err)
	require.Equal(t, http.StatusOK, got.status)
}

// getInGoroutine calls http.Get with a context.Context in scope so the
// instrumentation injects it into the request. It runs on a goroutine that does
// not hold the parent span in its GLS, so correct parenting proves the context
// propagated through the surrounding scope rather than GLS. The result is sent
// back to the test goroutine, which performs the fatal assertions.
func getInGoroutine(ctx context.Context, url string, res chan<- clientResult) {
	resp, err := http.Get(url)
	if err != nil {
		res <- clientResult{err: err}
		return
	}
	resp.Body.Close()
	res <- clientResult{status: resp.StatusCode}
}

func (tc *TestCaseClientGoroutine) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":     "http.request",
						"resource": "GET /hit",
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
								"resource": "GET /hit",
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
	}
}

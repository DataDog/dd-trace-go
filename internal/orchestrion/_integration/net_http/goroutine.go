// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package nethttp

// TestCaseClientGoroutine verifies trace correlation for the net/http client
// shorthands (http.Get, ...) when the call happens on a goroutine that received
// a context.Context as a parameter. This distinguishes call-site context
// injection (which works across goroutines) from GLS-based propagation (which
// does not cross goroutine boundaries): the parent span is started on the test
// goroutine, so it is absent from the spawned goroutine's GLS, and correct
// parenting can only come from the injected context.

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type TestCaseClientGoroutine struct {
	base
}

func (tc *TestCaseClientGoroutine) Setup(ctx context.Context, t *testing.T) {
	tc.handler = tc.serveMuxHandler()
	tc.base.Setup(ctx, t)
}

func (tc *TestCaseClientGoroutine) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	done := make(chan struct{})
	go getInGoroutine(ctx, t, "http://"+tc.srv.Addr+"/hit", done)
	<-done
}

// getInGoroutine calls http.Get with a context.Context in scope so the
// instrumentation injects it into the request. It runs on a goroutine that does
// not hold the parent span in its GLS, so correct parenting proves the context
// propagated through the surrounding scope rather than GLS.
func getInGoroutine(ctx context.Context, t *testing.T, url string, done chan<- struct{}) {
	defer close(done)
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
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

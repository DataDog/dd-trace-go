// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package nethttp

import (
	"context"
	"net/http"
	"testing"

	"example.com/httpdep"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseDependency checks that a client shorthand called in a dependency
// module, not in the module being built, gets the caller's context. The call
// runs on its own goroutine, so the parent can only come from that context.
type TestCaseDependency struct {
	base
}

func (tc *TestCaseDependency) Setup(ctx context.Context, t *testing.T) {
	tc.handler = tc.serveMuxHandler()
	tc.base.Setup(ctx, t)
}

func (tc *TestCaseDependency) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	res := make(chan clientResult, 1)
	go func() {
		resp, err := httpdep.Get(ctx, "http://"+tc.srv.Addr+"/hit")
		if err != nil {
			res <- clientResult{err: err}
			return
		}
		resp.Body.Close()
		res <- clientResult{status: resp.StatusCode}
	}()
	got := <-res
	require.NoError(t, got.err)
	require.Equal(t, http.StatusOK, got.status)
}

func (*TestCaseDependency) ExpectedTraces() trace.Traces {
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

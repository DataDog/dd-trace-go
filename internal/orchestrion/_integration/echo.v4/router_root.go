// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package echo

import (
	"context"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseRouterRoot asserts the router span is the trace root when
// DD_TRACE_HTTP_ROUTER_ROOT_SPAN is enabled (see issue #3369).
type TestCaseRouterRoot struct {
	TestCase
}

func (*TestCaseRouterRoot) PreBootstrap(_ context.Context, t *testing.T) {
	t.Setenv("DD_TRACE_HTTP_ROUTER_ROOT_SPAN", "true")
}

func (tc *TestCaseRouterRoot) ExpectedTraces() trace.Traces {
	httpURL := "http://" + tc.addr + "/ping"
	return trace.Traces{
		{
			Tags: map[string]any{
				"name":     "http.request",
				"resource": "GET /ping",
				"service":  "echo.v4.test",
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
						"service":  "echo",
						"resource": "GET /ping",
						"type":     "web",
					},
					Meta: map[string]string{
						"http.url":  httpURL,
						"component": "labstack/echo.v4",
						"span.kind": "server",
					},
				},
			},
		},
	}
}

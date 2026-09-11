// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package gorilla_mux

import (
	"context"
	"fmt"
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
	url := fmt.Sprintf("http://%s/ping", tc.Server.Addr)
	return trace.Traces{
		{
			Tags: map[string]any{
				"name":     "http.request",
				"resource": "GET /ping",
				"type":     "http",
				"service":  "gorilla_mux.test",
			},
			Meta: map[string]string{
				"http.url":  url,
				"component": "net/http",
				"span.kind": "client",
			},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":     "http.request",
						"resource": "GET /ping",
						"type":     "web",
						"service":  "mux.router",
					},
					Meta: map[string]string{
						"http.url":  url,
						"component": "gorilla/mux",
						"span.kind": "server",
					},
					Children: nil,
				},
			},
		},
	}
}

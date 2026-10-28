// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package julienschmidt_httprouter

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/maintest"
)

// TestRoutersInMain covers httprouter routers served from package main,
// including one imported under another name, and checks that each request
// gets exactly one httprouter span. The harness suites never build a package
// main.
func TestRoutersInMain(t *testing.T) {
	agent := maintest.Run(t, "./julienschmidt_httprouter/mainapp")
	lister, ok := agent.Agent.(agenttest.SpanLister)
	require.True(t, ok, "the test agent must list its spans")
	routerSpans := map[string]int{}
	for _, s := range lister.Spans() {
		if s.Meta["component"] == "julienschmidt/httprouter" {
			routerSpans[s.Resource]++
		}
	}
	for resource, status := range map[string]string{
		"GET /main/ping":      "200",
		"GET /main/users/:id": "200",
		"GET /main/fail":      "500",
		"GET /main/alias":     "200",
	} {
		agent.RequireSpan(t, agenttest.With().
			Operation("http.request").
			Resource(resource).
			Service("http.router").
			Tag("component", "julienschmidt/httprouter").
			Tag("span.kind", "server").
			Tag("http.status_code", status))
		require.Equal(t, 1, routerSpans[resource], "httprouter spans for %s", resource)
	}
	require.Len(t, routerSpans, 4, "httprouter spans by resource: %v", routerSpans)
}

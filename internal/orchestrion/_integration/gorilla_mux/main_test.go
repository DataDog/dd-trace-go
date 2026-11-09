// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package gorilla_mux

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/maintest"
)

// TestRoutersInMain covers gorilla/mux routers created in package main, and
// checks each request produces exactly one gorilla/mux span. The harness suites
// never build a package main, and match spans by subset.
func TestRoutersInMain(t *testing.T) {
	agent := maintest.Run(t, "./gorilla_mux/mainapp")
	lister, ok := agent.Agent.(agenttest.SpanLister)
	require.True(t, ok, "the test agent must list its spans")
	muxSpans := map[string]int{}
	for _, s := range lister.Spans() {
		if s.Meta["component"] == "gorilla/mux" {
			muxSpans[s.Resource]++
		}
	}
	for _, resource := range []string{
		"GET /main/router/{id}",
		"GET /main/sub/ping",
	} {
		agent.RequireSpan(t, agenttest.With().
			Operation("http.request").
			Resource(resource).
			Service("mux.router").
			Tag("component", "gorilla/mux"))
		require.Equal(t, 1, muxSpans[resource], "gorilla/mux spans for %s", resource)
	}
	require.Len(t, muxSpans, 2, "gorilla/mux spans: %v", muxSpans)
}

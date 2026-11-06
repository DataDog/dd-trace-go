// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package gin

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/maintest"
)

// TestEnginesInMain covers gin engines created in package main, including one
// imported under another name. The harness suites never build a package main.
func TestEnginesInMain(t *testing.T) {
	agent := maintest.Run(t, "./gin/mainapp")
	lister, ok := agent.Agent.(agenttest.SpanLister)
	require.True(t, ok, "the test agent must list its spans")
	ginSpans := map[string]int{}
	for _, s := range lister.Spans() {
		if s.Meta["component"] == "gin-gonic/gin" {
			ginSpans[s.Resource]++
		}
	}
	for _, resource := range []string{
		"GET /main/new",
		"GET /main/default",
		"GET /main/alias",
	} {
		agent.RequireSpan(t, agenttest.With().
			Operation("http.request").
			Resource(resource).
			Tag("component", "gin-gonic/gin"))
		require.Equal(t, 1, ginSpans[resource], "gin spans for %s", resource)
	}
}

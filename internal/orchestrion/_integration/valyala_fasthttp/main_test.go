// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package fasthttp

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/maintest"
)

// TestServersInMain covers a binary whose main package does not import the
// contrib. The suites in this package import it themselves, which would hide a
// build that fails to link the contrib.
func TestServersInMain(t *testing.T) {
	agent := maintest.Run(t, "./valyala_fasthttp/mainapp")
	lister, ok := agent.Agent.(agenttest.SpanLister)
	require.True(t, ok, "the test agent must list its spans")
	resources := []string{"GET /server", "GET /func"}
	for _, resource := range resources {
		agent.RequireSpan(t, agenttest.With().
			Operation("http.request").
			Resource(resource).
			Tag("component", "valyala/fasthttp").
			Tag("span.kind", "server"))
	}
	// One server span per request and nothing else.
	require.Len(t, lister.Spans(), len(resources))
}

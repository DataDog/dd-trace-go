// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package nethttp

import (
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/maintest"
)

// TestClientShorthandsInMain covers the client shorthand rules in package
// main, which the harness suites never build.
func TestClientShorthandsInMain(t *testing.T) {
	agent := maintest.Run(t, "./net_http/mainapp")
	root := agent.RequireSpan(t, agenttest.With().Operation("mainapp.root"))
	for _, resource := range []string{
		"GET /get",
		"HEAD /head",
		"POST /post",
		"POST /postform",
	} {
		agent.RequireSpan(t, agenttest.With().
			Operation("http.request").
			Resource(resource).
			Tag("span.kind", "client").
			ParentOf(uint64(root.SpanID)))
	}
}

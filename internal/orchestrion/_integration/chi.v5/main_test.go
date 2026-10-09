// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package chiv5

import (
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/maintest"
)

// TestRoutersInMain covers chi routers created in package main, including one
// imported under another name. The harness suites never build a package main.
func TestRoutersInMain(t *testing.T) {
	agent := maintest.Run(t, "./chi.v5/mainapp")
	for _, resource := range []string{
		"GET /v5/router",
		"GET /v5/mux",
		"GET /v5/alias",
	} {
		agent.RequireSpan(t, agenttest.With().
			Operation("http.request").
			Resource(resource).
			Tag("component", "go-chi/chi.v5"))
	}
}

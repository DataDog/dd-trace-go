// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package fiber

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/maintest"
)

// TestAppsInMain covers Fiber apps created in package main, including one in a
// package-level initializer and one imported under another name. The harness
// suites never build a package main.
func TestAppsInMain(t *testing.T) {
	agent := maintest.Run(t, "./fiber.v2/mainapp")
	resources := []string{"GET /main", "GET /var", "GET /alias"}
	for _, resource := range resources {
		agent.RequireSpan(t, agenttest.With().
			Operation("http.request").
			Resource(resource).
			Tag("component", "gofiber/fiber.v2").
			Tag("span.kind", "server"))
	}
	// One server span per request and nothing else.
	require.Equal(t, len(resources), agent.CountSpans())
}

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package chiv5

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/otelcrun"
)

// TestRoutersInMain covers the otelc rules with target: main. The harness
// suites only reach the $root rules, since the package under test is never
// main.
func TestRoutersInMain(t *testing.T) {
	agent := otelcrun.Run(t, "./chi.v5/mainapp")
	for _, resource := range []string{
		"GET /v5/router",
		"GET /v5/mux",
		"GET /v5/alias",
	} {
		assert.Truef(t, agent.Reported(resource),
			"no %q span reached the agent across %d payload(s)", resource, agent.RequestCount())
	}
}

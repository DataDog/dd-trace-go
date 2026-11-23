// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/maintest"
)

// TestMainPackageRules is the only check of the otelc rules that apply to a
// package main: the tracer start rule in ddtrace/tracer/otelc.yaml, and
// //dd:span in the file the tracer start rule rewrites. Every other suite runs
// under the integration harness, which builds no package main and calls
// tracertest.Bootstrap itself, so it has a running tracer either way.
//
// The plain build is the control: without it, a passing otelc case would only
// show that a span reached an agent, not that otelc started the tracer.
func TestMainPackageRules(t *testing.T) {
	maintest.RequireOtelc(t)

	t.Run("otelc build", func(t *testing.T) {
		agent := run(t, true)
		for _, name := range []string{spanName, directiveSpan, mainSpan} {
			assert.NotNilf(t, agent.FindSpan(agenttest.With().Operation(name)),
				"no %q span reached the agent across %d request(s)", name, agent.Requests())
		}
	})

	t.Run("plain build starts nothing", func(t *testing.T) {
		agent := run(t, false)
		assert.Nilf(t, agent.FindSpan(agenttest.With().Operation(spanName)),
			"a plain build reported %q, so a passing otelc case could not be attributed to otelc",
			spanName)
		assert.Zerof(t, agent.Requests(),
			"a plain build talked to the agent %d time(s); no tracer should have started",
			agent.Requests())
	})
}

// run builds the command, with otelc when instrument is true, and runs it
// against a new agent. No polling is needed: the app's own `defer
// tracer.Stop()` flushes synchronously, and the process has exited.
func run(t *testing.T, instrument bool) *maintest.Agent {
	bin := maintest.Build(t, "./otelc-autostart", instrument)
	agent := maintest.NewAgent(t)
	t.Logf("app output:\n%s", agent.Exec(t, bin))
	return agent
}

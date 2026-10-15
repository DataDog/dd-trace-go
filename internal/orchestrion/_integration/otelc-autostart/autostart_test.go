// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/maintest"
)

// TestOtelcStartsTracer is the only check that the tracer lifecycle rule in
// ddtrace/tracer/otelc.yaml works. Every other suite runs under the integration
// harness, which calls tracertest.Bootstrap itself and so has a running tracer
// either way.
//
// The plain build is the control: without it, a passing otelc case would only
// show that a span reached an agent, not that otelc started the tracer.
func TestOtelcStartsTracer(t *testing.T) {
	maintest.RequireOtelc(t)

	for _, tc := range []struct {
		name       string
		instrument bool
		wantSpan   bool
	}{
		{name: "otelc build starts the tracer", instrument: true, wantSpan: true},
		{name: "plain build starts nothing", instrument: false, wantSpan: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := maintest.Build(t, "./otelc-autostart", tc.instrument)
			agent := maintest.NewAgent(t)
			t.Logf("app output:\n%s", agent.Exec(t, bin))

			// No polling either way: the app's own `defer tracer.Stop()` flushes
			// synchronously, and the child process has already exited.
			span := agent.FindSpan(agenttest.With().Operation(spanName))
			if tc.wantSpan {
				assert.NotNilf(t, span,
					"no %q span reached the agent across %d request(s): otelc did not start the tracer",
					spanName, agent.Requests())
				return
			}
			assert.Nilf(t, span,
				"a plain build reported %q, so a passing otelc case could not be attributed to otelc",
				spanName)
			assert.Zerof(t, agent.Requests(),
				"a plain build talked to the agent %d time(s); no tracer should have started",
				agent.Requests())
		})
	}
}

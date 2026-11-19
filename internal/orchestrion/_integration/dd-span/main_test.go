// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package ddspan

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/maintest"
	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

// TestAnnotatedMain checks that a //dd:span on func main is sent with its
// children: the tracer must start before the main span and stop after it
// finishes.
func TestAnnotatedMain(t *testing.T) {
	if otelc.Enabled() {
		t.Skip("otelc runs the //dd:span code before the injected tracer start: open-telemetry/opentelemetry-go-compile-instrumentation#583")
	}
	bin := maintest.BuildWithOrchestrion(t, "./dd-span/mainapp")
	agent := maintest.NewAgent(t)
	t.Logf("app output:\n%s", agent.Exec(t, bin))

	main := agent.FindSpan(agenttest.With().Operation("ddspan.main"))
	require.NotNilf(t, main, "no main span across %d request(s)", agent.Requests())
	child := agent.FindSpan(agenttest.With().Operation("ddspan.main.child"))
	require.NotNilf(t, child, "no child span across %d request(s)", agent.Requests())
	assert.Equal(t, main.SpanID, child.ParentID)
}

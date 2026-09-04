// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

// TestParseDecisionMaker_MalformedValue_ReportsWellFormedError proves
// parseDecisionMaker's LogAndReportError call produces a well-formed Error
// Tracking payload when given a malformed decision-maker value (e.g. from an
// inbound _dd.p.dm propagation tag) — the same defect
// internal/apps/telemetry-errors's DecisionMakerHandler triggers manually via
// HTTP, verified here in-process with no HTTP server or agent needed. See
// internal/apps/telemetry-errors/README.md's tier-0 checklist.
func TestParseDecisionMaker_MalformedValue_ReportsWellFormedError(t *testing.T) {
	client, rt := telemetrytest.NewCapturingClient(t)
	defer telemetry.MockClient(client)()

	got := parseDecisionMaker("zz")
	assert.Equal(t, uint32(0), got)

	client.Flush()

	logs := rt.LogMessages()
	require.Len(t, logs, 1)
	msg := logs[0]

	// message_match
	assert.True(t, strings.HasPrefix(msg.Message, "failed to convert decision maker to uint32"),
		"message: %s", msg.Message)
	// error_type_present
	assert.Contains(t, msg.Message, "error.error_type=strconv.NumError")
	assert.Equal(t, telemetry.LogError, msg.Level)
	assert.EqualValues(t, 1, msg.Count)
	// stack_points_at_call_site / no_replay_frames
	assert.Contains(t, msg.StackTrace, "parseDecisionMaker")
	assert.NotContains(t, msg.StackTrace, "Replay")
	assert.NotContains(t, msg.StackTrace, "globalClientRecorder")
	assert.NotContains(t, msg.StackTrace, "SwapClient")
}

// TestParseDecisionMaker_MalformedValue_DedupCount proves repeated failures
// with the same constant message dedup into a single log entry with an
// incremented count, matching the harness's "count" check.
func TestParseDecisionMaker_MalformedValue_DedupCount(t *testing.T) {
	client, rt := telemetrytest.NewCapturingClient(t)
	defer telemetry.MockClient(client)()

	parseDecisionMaker("zz")
	parseDecisionMaker("also-not-an-int")
	client.Flush()

	logs := rt.LogMessages()
	require.Len(t, logs, 1)
	assert.EqualValues(t, 2, logs[0].Count)
}

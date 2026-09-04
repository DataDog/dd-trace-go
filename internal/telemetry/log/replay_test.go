// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package log

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/internal/transport"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

// TestReportError_QueuedBeforeClientExists_StackPointsAtCallSite proves the
// fix for the replay-time stack capture bug: a report made before any
// telemetry client exists gets queued by globalClientCall, then replayed
// once a client is installed via SwapClient. Before WithStacktraceNow, the
// stack trace attached to the eventual wire payload pointed at the replay
// machinery (SwapClient/Replay/globalClientRecorder), not at this test's own
// call site, because capture was deferred all the way to loggerBackend.add,
// which only runs at replay time in this scenario.
func TestReportError_QueuedBeforeClientExists_StackPointsAtCallSite(t *testing.T) {
	telemetry.StopApp()
	t.Cleanup(telemetry.StopApp)

	require.Nil(t, telemetry.GlobalClient(), "must run before any client exists to exercise the replay path")

	ReportError("dogfood test: queued before a client existed", errors.New("boom"))

	client, rt := telemetrytest.NewCapturingClient(t)
	defer client.Close()

	// SwapClient (not StartApp) replays the queue synchronously and skips
	// AppStart plus the async flush goroutine StartApp also kicks off,
	// keeping this test deterministic and focused on exactly the replay
	// mechanism under test.
	telemetry.SwapClient(client)
	client.Flush()

	logs := rt.LogMessages()
	require.Len(t, logs, 1)

	stack := logs[0].StackTrace
	assert.Contains(t, stack, "TestReportError_QueuedBeforeClientExists_StackPointsAtCallSite",
		"stack must show this test's own call site")
	assert.NotContains(t, stack, "Replay", "must not show the replay machinery")
	assert.NotContains(t, stack, "SwapClient")
	assert.NotContains(t, stack, "globalClientRecorder")
}

// TestReportError_DoesNotDedupIntoPlainLogEntry proves the fix for the
// report-dedup collision: a plain, stackless telemetrylog.Error with the same
// message, level, and tags as a report shares the backend's dedup key shape,
// so before the key carried a stack-now marker the report merged into the
// plain entry and lost both its stack trace and its error attribute.
func TestReportError_DoesNotDedupIntoPlainLogEntry(t *testing.T) {
	telemetry.StopApp()
	t.Cleanup(telemetry.StopApp)

	require.Nil(t, telemetry.GlobalClient(), "must run before any client exists to exercise the replay path")

	// Same message, level, and (empty) tags: a guaranteed key collision
	// between a plain log entry and a report if the key has no report marker.
	Error("dogfood test: colliding message")
	ReportError("dogfood test: colliding message", errors.New("boom"))

	client, rt := telemetrytest.NewCapturingClient(t)
	defer client.Close()

	telemetry.SwapClient(client)
	client.Flush()

	logs := rt.LogMessages()
	require.Len(t, logs, 2, "the plain log and the report must be two distinct entries")

	var stackless, report transport.LogMessage
	for _, msg := range logs {
		if msg.StackTrace == "" {
			stackless = msg
		} else {
			report = msg
		}
	}
	assert.Equal(t, "dogfood test: colliding message", stackless.Message,
		"the plain entry must stay attribute-free")
	assert.NotEmpty(t, report.StackTrace, "the report entry must keep its captured stack")
	assert.Contains(t, report.Message, "error.error_type=", "the report entry must keep its error attribute")
	assert.Contains(t, report.StackTrace, "TestReportError_DoesNotDedupIntoPlainLogEntry",
		"the report's stack must show this test's call site")
}

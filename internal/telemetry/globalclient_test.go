// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package telemetry

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/internal/transport"
)

// Ensure that DD_INSTRUMENTATION_TELEMETRY_ENABLED is read once and cached,
// matching the expectation that env vars are set before telemetry is first used.
func TestDisabledCachesInitialEnv(t *testing.T) {
	// Reset lazy init state
	telemetryEnabledOnce = sync.Once{}

	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "0")
	require.True(t, Disabled())

	// Changing the env after the first call should not flip the cached value.
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "1")
	require.True(t, Disabled())

	// Reset again
	telemetryEnabledOnce = sync.Once{}
}

// TestLog_QueuedBeforeStartApp_CapturesCallSiteStacktrace reproduces the bug
// WithStacktrace() actually captures nothing itself — the real capture used
// to happen inside loggerBackend.add, at replay time for a call queued
// before StartApp. That attributed the stack to the replay goroutine
// (SwapClient/globalClientRecorder.Replay), not to this test function, the
// call's real site. Log now captures synchronously before queuing, and this
// asserts the rendered stack reflects that: no replay-machinery frames
// (SwapClient/Replay/globalClientCall), just this test function.
func TestLog_QueuedBeforeStartApp_CapturesCallSiteStacktrace(t *testing.T) {
	telemetryEnabledOnce = sync.Once{}
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "1")
	t.Cleanup(func() { telemetryEnabledOnce = sync.Once{} })

	globalClientRecorder.Clear()
	require.Nil(t, GlobalClient(), "no client must be installed yet for this test to exercise the queued path")

	// The exact bug scenario: call the package-level Log function before any
	// client exists, so globalClientCall queues the closure instead of
	// running it immediately.
	Log(NewRecord(LogError, "queued before start"), WithStacktrace())

	tracerConfig := internal.TracerConfig{Service: "test-service", Env: "test-env", Version: "1.0.0"}
	config := defaultConfig(ClientConfig{})
	config.AgentURL = "http://localhost:8126"
	config.FlushInterval = internal.Range[time.Duration]{Min: time.Hour, Max: time.Hour}
	c, err := newClient(tracerConfig, config)
	require.NoError(t, err)
	t.Cleanup(func() { c.Close() })
	t.Cleanup(func() { SwapClient(nil) })

	recordWriter := &internal.RecordWriter{}
	c.writer = recordWriter

	// Installing the client replays the queued Log call — on this goroutine,
	// not the original call site's.
	old := SwapClient(c)
	require.Nil(t, old)

	c.Flush()

	payloads := recordWriter.Payloads()
	require.NotEmpty(t, payloads)
	require.IsType(t, transport.Logs{}, payloads[0])
	logs := payloads[0].(transport.Logs)
	require.Len(t, logs.Logs, 1)

	stack := logs.Logs[0].StackTrace
	assert.Contains(t, stack, "TestLog_QueuedBeforeStartApp_CapturesCallSiteStacktrace")
	assert.NotContains(t, stack, "SwapClient")
	assert.NotContains(t, stack, "Replay")
	assert.NotContains(t, stack, "globalClientCall")
}

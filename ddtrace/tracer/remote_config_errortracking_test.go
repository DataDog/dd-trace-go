// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"errors"
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
	internalffe "github.com/DataDog/dd-trace-go/v2/internal/openfeature"
	"github.com/DataDog/dd-trace-go/v2/internal/remoteconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func useLocalLogCapture(t *testing.T) *log.RecordLogger {
	t.Helper()

	localLogs := new(log.RecordLogger)
	restoreLogger := log.UseLogger(localLogs)
	t.Cleanup(func() {
		log.Flush()
		restoreLogger()
	})
	return localLogs
}

func TestRemoteConfigDisabledOpenFeatureSubscriptionLogsLocallyWithoutReporting(t *testing.T) {
	t.Setenv("DD_REMOTE_CONFIGURATION_ENABLED", "false")
	t.Setenv("DD_FEATURE_FLAGS_CONFIGURATION_SOURCE", "remote_config")
	t.Cleanup(remoteconfig.Reset)
	internalffe.ResetForTest()
	t.Cleanup(internalffe.ResetForTest)

	localLogs := useLocalLogCapture(t)
	client, capture := telemetrytest.NewCapturingClient(t)
	defer client.Close()
	defer telemetry.MockClient(client)()

	tr, _, _, stop, err := startTestTracer(t, WithService("my-service"), WithEnv("my-env"))
	require.NoError(t, err)
	defer stop()

	err = tr.startRemoteConfig(remoteconfig.DefaultClientConfig())
	require.ErrorIs(t, err, remoteconfig.ErrClientNotStarted)

	log.Flush()
	client.Flush()
	assert.Contains(t, strings.Join(localLogs.Logs(), "\n"), "openfeature: failed to subscribe to Remote Config: remote config client not started")
	assert.Empty(t, capture.LogMessages())
}

func TestRemoteConfigSubscriptionErrorsReportedToTelemetry(t *testing.T) {
	t.Setenv("DD_REMOTE_CONFIGURATION_ENABLED", "false")
	t.Cleanup(remoteconfig.Reset)

	localLogs := useLocalLogCapture(t)
	client, capture := telemetrytest.NewCapturingClient(t)
	defer client.Close()
	defer telemetry.MockClient(client)()

	tr, _, _, stop, err := startTestTracer(t, WithService("my-service"), WithEnv("my-env"))
	require.NoError(t, err)
	defer stop()

	// The remote config client is not started, so the Dynamic Instrumentation
	// enable path fails when its subscriptions are attempted.
	enabled := true
	tr.handleDynamicInstrumentationEnabledRC(&enabled)

	// A stale token makes the disable path fail when it unsubscribes. The
	// enable path left both real tokens at zero because the subscriptions
	// never started.
	tr.dynInstSubscriptions.mu.Lock()
	tr.dynInstSubscriptions.ldSubscriptionToken = 1
	tr.dynInstSubscriptions.mu.Unlock()
	disabled := false
	tr.handleDynamicInstrumentationEnabledRC(&disabled)

	reportOpenFeatureSubscriptionError(errors.New("subscribe failed"))

	client.Flush()
	log.Flush()

	localLogMessages := strings.Join(localLogs.Logs(), "\n")
	assert.Contains(t, localLogMessages, "failed to start Dynamic Instrumentation subscriptions: remote config client not started")
	assert.Contains(t, localLogMessages, "failed to stop Dynamic Instrumentation subscriptions: remote config client not started")
	assert.Contains(t, localLogMessages, "openfeature: failed to subscribe to Remote Config: subscribe failed")

	logs := capture.LogMessages()
	require.Len(t, logs, 3)
	type observedLog struct {
		message    string
		level      string
		count      uint32
		stackTrace string
	}
	messages := make(map[string]observedLog, len(logs))
	for _, entry := range logs {
		message, _, _ := strings.Cut(entry.Message, ": error.error_type=")
		messages[message] = observedLog{
			message:    entry.Message,
			level:      string(entry.Level),
			count:      entry.Count,
			stackTrace: entry.StackTrace,
		}
	}

	// The error type per message: the enable path joins the two subscription
	// errors, the disable path wraps the single unsubscribe error, and the
	// OpenFeature site receives a plain error.
	for message, expected := range map[string]struct {
		callSite string
		errType  string
	}{
		"failed to start Dynamic Instrumentation subscriptions": {"handleDynamicInstrumentationEnabledRC", "errors.joinError"},
		"failed to stop Dynamic Instrumentation subscriptions":  {"handleDynamicInstrumentationEnabledRC", "errors.errorString"},
		"openfeature: failed to subscribe to Remote Config":     {"reportOpenFeatureSubscriptionError", "errors.errorString"},
	} {
		entry, ok := messages[message]
		require.True(t, ok, "missing telemetry report for %q", message)
		assert.Equal(t, "ERROR", entry.level)
		assert.Equal(t, uint32(1), entry.count)
		assert.Contains(t, entry.message, "error.error_type="+expected.errType)
		assert.Contains(t, entry.stackTrace, "remote_config.go")
		assert.Contains(t, entry.stackTrace, expected.callSite)
	}
}

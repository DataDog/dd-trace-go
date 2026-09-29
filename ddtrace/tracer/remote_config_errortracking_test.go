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

func TestRemoteConfigDisabledOpenFeatureSubscriptionLogsLocallyWithoutReporting(t *testing.T) {
	t.Setenv("DD_REMOTE_CONFIGURATION_ENABLED", "false")
	t.Setenv("DD_FEATURE_FLAGS_CONFIGURATION_SOURCE", "remote_config")
	t.Cleanup(remoteconfig.Reset)
	internalffe.ResetForTest()
	t.Cleanup(internalffe.ResetForTest)

	localLogs := new(log.RecordLogger)
	defer log.UseLogger(localLogs)()
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
	client, capture := telemetrytest.NewCapturingClient(t)
	defer client.Close()
	defer telemetry.MockClient(client)()

	reportDynamicInstrumentationStartError(errors.New("start failed"))
	reportDynamicInstrumentationStopError(errors.New("stop failed"))
	reportOpenFeatureSubscriptionError(errors.New("subscribe failed"))
	client.Flush()

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

	for message, callSite := range map[string]string{
		"failed to start Dynamic Instrumentation subscriptions": "reportDynamicInstrumentationStartError",
		"failed to stop Dynamic Instrumentation subscriptions":  "reportDynamicInstrumentationStopError",
		"openfeature: failed to subscribe to Remote Config":     "reportOpenFeatureSubscriptionError",
	} {
		entry, ok := messages[message]
		require.True(t, ok, "missing telemetry report for %q", message)
		assert.Equal(t, "ERROR", entry.level)
		assert.Equal(t, uint32(1), entry.count)
		assert.Contains(t, entry.message, "error.error_type=errors.errorString")
		assert.Contains(t, entry.stackTrace, "remote_config.go")
		assert.Contains(t, entry.stackTrace, callSite)
	}
}

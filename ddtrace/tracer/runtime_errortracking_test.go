// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/otelmetricsinstall"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

func TestRuntimeStartupErrorsReportedToTelemetry(t *testing.T) {
	client, rt := telemetrytest.NewCapturingClient(t)
	defer telemetry.MockClient(client)()

	reportOtelRuntimeMetricsStartError(&otelmetricsinstall.RuntimeRegistrationError{Err: errors.New("register callback")})
	reportRemoteConfigStartupError(errors.New("create repository"))
	client.Flush()

	logs := rt.LogMessages()
	require.Len(t, logs, 2)
	// The two reports are captured in nondeterministic order, so each one is
	// located by its message prefix instead of its slice index.
	var sawOtel, sawRemoteConfig bool
	for _, l := range logs {
		switch {
		case strings.HasPrefix(l.Message, "Failed to start OTel runtime metrics"):
			sawOtel = true
			assert.Contains(t, l.Message, "error.error_type=errors.errorString")
			assert.Equal(t, telemetry.LogError, l.Level)
			assert.Contains(t, l.StackTrace, "reportOtelRuntimeMetricsStartError")
		case strings.HasPrefix(l.Message, "Remote config startup error"):
			sawRemoteConfig = true
			assert.Contains(t, l.Message, "error.error_type=errors.errorString")
			assert.Equal(t, telemetry.LogError, l.Level)
			assert.Contains(t, l.StackTrace, "reportRemoteConfigStartupError")
		default:
			t.Errorf("unexpected log message: %q", l.Message)
		}
	}
	require.True(t, sawOtel, "OTel runtime metrics report missing from captured logs")
	require.True(t, sawRemoteConfig, "remote config startup report missing from captured logs")
}

func TestExporterConfigurationErrorIsNotReportedToTelemetry(t *testing.T) {
	client, rt := telemetrytest.NewCapturingClient(t)
	defer telemetry.MockClient(client)()

	reportOtelRuntimeMetricsStartError(errors.New("invalid exporter configuration"))
	client.Flush()

	assert.Empty(t, rt.LogMessages())
}

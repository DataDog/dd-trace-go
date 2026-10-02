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
	assert.True(t, strings.HasPrefix(logs[0].Message, "Failed to start OTel runtime metrics"))
	assert.Contains(t, logs[0].Message, "error.error_type=errors.errorString")
	assert.Equal(t, telemetry.LogError, logs[0].Level)
	assert.Contains(t, logs[0].StackTrace, "reportOtelRuntimeMetricsStartError")
	assert.True(t, strings.HasPrefix(logs[1].Message, "Remote config startup error"))
	assert.Contains(t, logs[1].Message, "error.error_type=errors.errorString")
	assert.Equal(t, telemetry.LogError, logs[1].Level)
	assert.Contains(t, logs[1].StackTrace, "reportRemoteConfigStartupError")
}

func TestExporterConfigurationErrorIsNotReportedToTelemetry(t *testing.T) {
	client, rt := telemetrytest.NewCapturingClient(t)
	defer telemetry.MockClient(client)()

	reportOtelRuntimeMetricsStartError(errors.New("invalid exporter configuration"))
	client.Flush()

	assert.Empty(t, rt.LogMessages())
}

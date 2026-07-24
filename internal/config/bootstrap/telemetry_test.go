// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package bootstrap

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTelemetryEnabledCachesFirstRead(t *testing.T) {
	ResetForTesting()
	t.Cleanup(ResetForTesting)

	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "0")
	require.False(t, TelemetryEnabled())

	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "1")
	require.False(t, TelemetryEnabled())
}

func TestTelemetryEnabledDefaultsTrue(t *testing.T) {
	ResetForTesting()
	t.Cleanup(ResetForTesting)
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "temporary")
	require.NoError(t, os.Unsetenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED"))

	require.True(t, TelemetryEnabled())
}

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package bootstrap

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
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

func TestTelemetryEnabledValueSemanticsAndWarnings(t *testing.T) {
	const key = "DD_INSTRUMENTATION_TELEMETRY_ENABLED"
	require.True(t, parseTelemetryEnabledValue("", false))

	tests := []struct {
		name    string
		raw     string
		present bool
		want    bool
		warn    bool
	}{
		{name: "absent", want: true},
		{name: "valid true", raw: "TRUE", present: true, want: true},
		{name: "valid false", raw: "0", present: true},
		{name: "invalid", raw: "invalid", present: true, want: true, warn: true},
		{name: "explicit empty", present: true, want: true, warn: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ResetForTesting()
			t.Cleanup(ResetForTesting)
			old, wasPresent := os.LookupEnv(key)
			require.NoError(t, os.Unsetenv(key))
			t.Cleanup(func() {
				if wasPresent {
					require.NoError(t, os.Setenv(key, old))
				} else {
					require.NoError(t, os.Unsetenv(key))
				}
			})
			if tt.present {
				require.NoError(t, os.Setenv(key, tt.raw))
			}
			logger := new(log.RecordLogger)
			defer log.UseLogger(logger)()

			require.Equal(t, tt.want, TelemetryEnabled())
			require.Equal(t, tt.want, TelemetryEnabled(), "the first result must be cached")
			logs := strings.Join(logger.Logs(), "\n")
			if tt.warn {
				require.Contains(t, logs, "Non-boolean value for env var "+key+". Parse failed with error:")
				require.Len(t, logger.Logs(), 1, "a cached invalid value must warn once")
			} else {
				require.Empty(t, logger.Logs())
			}
		})
	}
}

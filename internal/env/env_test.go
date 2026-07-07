// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package env

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVerifySupportedConfiguration(t *testing.T) {
	// Known configuration - with alias

	t.Run("Known configuration - with alias", func(t *testing.T) {
		res, ok := Lookup("DD_API_KEY")
		require.False(t, ok)
		require.Equal(t, "", res)

		res, ok = Lookup("DD-API-KEY")
		require.False(t, ok)
		require.Equal(t, "", res)

		t.Setenv("DD-API-KEY", "VALUE")
		res, ok = Lookup("DD-API-KEY")
		require.True(t, ok)
		require.Equal(t, "VALUE", res)

		res = Get("DD-API-KEY")
		require.Equal(t, "VALUE", res)

		res, ok = Lookup("DD_API_KEY")
		require.True(t, ok)
		require.Equal(t, "VALUE", res)

		res = Get("DD_API_KEY")
		require.Equal(t, "VALUE", res)
	})

	t.Run("Known configuration - without alias", func(t *testing.T) {
		t.Setenv("DD_SERVICE", "TEST_SERVICE")
		res, ok := Lookup("DD_SERVICE")
		require.True(t, ok)
		require.Equal(t, "TEST_SERVICE", res)

		res = Get("DD_SERVICE")
		require.Equal(t, "TEST_SERVICE", res)
	})

	t.Run("sensitive configuration", func(t *testing.T) {
		// OTLP header variants are seeded as sensitive in supported_configurations.json.
		require.True(t, IsSensitive("OTEL_EXPORTER_OTLP_HEADERS"))
		require.True(t, IsSensitive("OTEL_EXPORTER_OTLP_TRACES_HEADERS"))
		require.True(t, IsSensitive("OTEL_EXPORTER_OTLP_METRICS_HEADERS"))
		require.True(t, IsSensitive("OTEL_EXPORTER_OTLP_LOGS_HEADERS"))

		// DD_API_KEY is seeded as sensitive, and its alias resolves to the same result.
		require.True(t, IsSensitive("DD_API_KEY"))
		require.True(t, IsSensitive("DD-API-KEY"))
		require.True(t, IsSensitive("DD_APP_KEY"))

		// Non-sensitive configurations are not flagged.
		require.False(t, IsSensitive("DD_SERVICE"))
		require.False(t, IsSensitive("OTEL_EXPORTER_OTLP_ENDPOINT"))
	})

	t.Run("unknown configuration", func(t *testing.T) {
		// Unknown configuration that would be added to the supported configurations file.
		t.Setenv("DD_UNKNOWN_CONFIGURATION_KEY", "VALUE")
		res, ok := Lookup("DD_UNKNOWN_CONFIGURATION_KEY")
		require.False(t, ok)
		require.Empty(t, res)

		res = Get("DD_UNKNOWN_CONFIGURATION_KEY")
		require.Empty(t, res)

		// Check that the env var has been added to the supported configurations file
		// acquire lock to read the file and remove the new key to avoid polluting
		// results with a false positive.
		mu.Lock()
		defer mu.Unlock()

		cfg, err := readSupportedConfigurations(getConfigFilePath())
		require.NoError(t, err)
		require.Contains(t, cfg.SupportedConfigurations, "DD_UNKNOWN_CONFIGURATION_KEY")
		defaultValue := "FIX_ME"
		require.EqualValues(t, []configurationImplementation{{
			Implementation: "A",
			Type:           defaultValue,
			Default:        &defaultValue,
		}}, cfg.SupportedConfigurations["DD_UNKNOWN_CONFIGURATION_KEY"])

		// Remove the env var from the supported configurations file
		delete(cfg.SupportedConfigurations, "DD_UNKNOWN_CONFIGURATION_KEY")
		err = writeSupportedConfigurations(getConfigFilePath(), cfg)
		require.NoError(t, err)
	})
}

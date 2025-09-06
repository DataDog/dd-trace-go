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
		require.Equal(t, []string{"A"}, cfg.SupportedConfigurations["DD_UNKNOWN_CONFIGURATION_KEY"])

		// Remove the env var from the supported configurations file
		delete(cfg.SupportedConfigurations, "DD_UNKNOWN_CONFIGURATION_KEY")
		err = writeSupportedConfigurations(getConfigFilePath(), cfg)
		require.NoError(t, err)
	})
}

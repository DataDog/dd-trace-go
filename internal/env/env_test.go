// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package env

import (
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
)

const privateRetryProcessSnapshotHelperEnv = "TEST_PRIVATE_RETRY_PROCESS_SNAPSHOT_HELPER"

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

func TestLookupPrivateOnlyAllowsRetryProcessTransportKeys(t *testing.T) {
	const nonTransportKey = "DD_PROCESS_RETRY_PRIVATE_TEST"
	const unknownTransportPrefixedKey = "DD_CIVISIBILITY_INTERNAL_RETRY_PROCESS_UNKNOWN"
	privateTransportKeys := []string{
		constants.CIVisibilityInternalRetryProcessChild,
		constants.CIVisibilityInternalRetryProcessResultPath,
		constants.CIVisibilityInternalRetryProcessTestName,
		constants.CIVisibilityInternalRetryProcessAttempt,
		constants.CIVisibilityInternalRetryProcessReason,
	}

	for _, key := range privateTransportKeys {
		t.Setenv(key, "private-value")

		value, ok := LookupPrivate(key)
		require.True(t, ok)
		require.Equal(t, "private-value", value)
	}
	t.Setenv(nonTransportKey, "non-transport-value")

	value, ok := LookupPrivate(nonTransportKey)
	require.False(t, ok)
	require.Empty(t, value)
	t.Setenv(unknownTransportPrefixedKey, "unknown-value")
	value, ok = LookupPrivate(unknownTransportPrefixedKey)
	require.False(t, ok)
	require.Empty(t, value)

	value, ok = os.LookupEnv(unknownTransportPrefixedKey)
	require.True(t, ok)
	require.Equal(t, "unknown-value", value)

	mu.Lock()
	defer mu.Unlock()

	cfg, err := readSupportedConfigurations(getConfigFilePath())
	require.NoError(t, err)
	for _, key := range privateTransportKeys {
		require.NotContains(t, cfg.SupportedConfigurations, key)
		require.NotContains(t, SupportedConfigurations, key)
	}
	require.NotContains(t, cfg.SupportedConfigurations, nonTransportKey)
	require.NotContains(t, cfg.SupportedConfigurations, unknownTransportPrefixedKey)
}

func TestPrivateRetryProcessStartupSnapshot(t *testing.T) {
	mode, _ := os.LookupEnv(privateRetryProcessSnapshotHelperEnv)
	privateValues := map[string]string{
		constants.CIVisibilityInternalRetryProcessChild:      "true",
		constants.CIVisibilityInternalRetryProcessResultPath: "result.json",
		constants.CIVisibilityInternalRetryProcessTestName:   "TestSelected",
		constants.CIVisibilityInternalRetryProcessAttempt:    "1",
		constants.CIVisibilityInternalRetryProcessReason:     constants.AutoTestRetriesRetryReason,
	}
	switch mode {
	case "snapshot":
		require.NoError(t, PrivateRetryProcessTransportError())
		for key, want := range privateValues {
			got, ok := LookupPrivate(key)
			require.True(t, ok)
			require.Equal(t, want, got)
			_, inherited := os.LookupEnv(key)
			require.False(t, inherited)
		}

		cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$")
		cmd.Env = append(os.Environ(), privateRetryProcessSnapshotHelperEnv+"=descendant")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, string(output))

		require.NoError(t, os.Setenv(constants.CIVisibilityInternalRetryProcessChild, "false"))
		t.Cleanup(func() { _ = os.Unsetenv(constants.CIVisibilityInternalRetryProcessChild) })
		got, ok := LookupPrivate(constants.CIVisibilityInternalRetryProcessChild)
		require.True(t, ok)
		require.Equal(t, "true", got)
		return
	case "descendant":
		for key := range privateValues {
			_, ok := LookupPrivate(key)
			require.False(t, ok)
			_, inherited := os.LookupEnv(key)
			require.False(t, inherited)
		}
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$")
	cmd.Env = append(os.Environ(), privateRetryProcessSnapshotHelperEnv+"=snapshot")
	for key, value := range privateValues {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))
}

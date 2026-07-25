// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package utils

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCIProviderEnvironmentBoundaryRejectsConfigurationNamespaces(t *testing.T) {
	for _, key := range []ciProviderEnvKey{
		"DD_SERVICE",
		"DD_PIPELINE_EXECUTION_ID",
		"DD-CIVISIBILITY-ENABLED",
		"OTEL_SERVICE_NAME",
	} {
		t.Run(string(key), func(t *testing.T) {
			t.Setenv(string(key), "must-not-be-read")
			value, present := lookupCIProviderEnvironment(key)
			require.Empty(t, value)
			require.False(t, present)
		})
	}
}

func TestCIProviderEnvironmentBoundaryReadsProviderMetadata(t *testing.T) {
	t.Setenv("GITHUB_SHA", "abc123")

	value, present := lookupCIProviderEnvironment("GITHUB_SHA")

	require.Equal(t, "abc123", value)
	require.True(t, present)
}

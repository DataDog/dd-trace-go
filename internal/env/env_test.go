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
	t.Setenv("DD-API-KEY", "VALUE")
	res, ok := LookupEnv("DD_API_KEY")
	require.True(t, ok)
	require.Equal(t, "VALUE", res)

	res = Getenv("DD_API_KEY")
	require.Equal(t, "VALUE", res)

	// Known configuration - without alias
	t.Setenv("DD_SERVICE", "TEST_SERVICE")
	res, ok = LookupEnv("DD_SERVICE")
	require.True(t, ok)
	require.Equal(t, "TEST_SERVICE", res)

	res = Getenv("DD_SERVICE")
	require.Equal(t, "TEST_SERVICE", res)

	// Unknown configuration with no adding to the supported configurations
	// file.
	t.Setenv("DD_CONFIG_INVERSION_UNKNOWN", "VALUE")
	res, ok = LookupEnv("DD_CONFIG_INVERSION_UNKNOWN")
	require.False(t, ok)
	require.Empty(t, res)
}

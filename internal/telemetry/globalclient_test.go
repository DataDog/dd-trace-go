// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package telemetry

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// Ensure that DD_INSTRUMENTATION_TELEMETRY_ENABLED is read once and cached,
// matching the expectation that env vars are set before telemetry is first used.
func TestDisabledCachesInitialEnv(t *testing.T) {
	// Reset lazy init state
	telemetryEnabledOnce = sync.Once{}

	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "0")
	require.True(t, Disabled())

	// Changing the env after the first call should not flip the cached value.
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "1")
	require.True(t, Disabled())

	// Reset again
	telemetryEnabledOnce = sync.Once{}
}

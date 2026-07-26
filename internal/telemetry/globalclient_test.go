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

func TestDisabledUsesSingletonConfiguration(t *testing.T) {
	previousLoader := instrumentationTelemetryLoader.Load().(instrumentationTelemetryEnabledLoader)
	previousEnabled := instrumentationTelemetryEnabled.Load()
	t.Cleanup(func() {
		instrumentationTelemetryLoader.Store(previousLoader)
		instrumentationTelemetryEnabled.Store(previousEnabled)
		instrumentationTelemetryOnce = sync.Once{}
	})
	instrumentationTelemetryOnce = sync.Once{}

	setInstrumentationTelemetryEnabled(false)
	require.True(t, Disabled())

	setInstrumentationTelemetryEnabled(true)
	require.True(t, Disabled())
}

func TestRuntimeFailureDisableIsSticky(t *testing.T) {
	previousLoader := instrumentationTelemetryLoader.Load().(instrumentationTelemetryEnabledLoader)
	previousEnabled := instrumentationTelemetryEnabled.Load()
	t.Cleanup(func() {
		instrumentationTelemetryLoader.Store(previousLoader)
		instrumentationTelemetryEnabled.Store(previousEnabled)
		instrumentationTelemetryOnce = sync.Once{}
	})
	instrumentationTelemetryOnce = sync.Once{}
	setInstrumentationTelemetryEnabled(true)
	require.False(t, Disabled())

	disableInstrumentationTelemetryAfterFailure()
	require.True(t, Disabled())

	require.True(t, Disabled())
}

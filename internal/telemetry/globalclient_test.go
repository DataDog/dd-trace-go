// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package telemetry

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDisabledUsesSingletonConfiguration(t *testing.T) {
	previous := instrumentationTelemetryEnabled.Load()
	t.Cleanup(func() {
		SetInstrumentationTelemetryEnabled(previous)
	})

	SetInstrumentationTelemetryEnabled(false)
	require.True(t, Disabled())

	SetInstrumentationTelemetryEnabled(true)
	require.False(t, Disabled())
}

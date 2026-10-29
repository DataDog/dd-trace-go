// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTraceOTelEnabled(t *testing.T) {
	t.Run("disabled by default", func(t *testing.T) {
		resetGlobalState()
		defer resetGlobalState()

		cfg := Get()
		require.NotNil(t, cfg)

		assert.False(t, cfg.TraceOTelEnabled())
	})

	t.Run("enabled via DD_TRACE_OTEL_ENABLED", func(t *testing.T) {
		resetGlobalState()
		defer resetGlobalState()

		t.Setenv("DD_TRACE_OTEL_ENABLED", "true")

		cfg := Get()
		require.NotNil(t, cfg)

		assert.True(t, cfg.TraceOTelEnabled())
	})
}

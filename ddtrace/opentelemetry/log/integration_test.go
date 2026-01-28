// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
	"context"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStart(t *testing.T) {
	t.Run("does nothing when DD_LOGS_OTEL_ENABLED=false", func(t *testing.T) {
		// Clean up any existing provider
		_ = ShutdownGlobalLoggerProvider(context.Background())

		// Ensure DD_LOGS_OTEL_ENABLED is false (default)
		config.SetUseFreshConfig(true)
		defer config.SetUseFreshConfig(false)

		err := Start(context.Background())
		assert.NoError(t, err)

		// Provider should not be initialized
		provider := GetGlobalLoggerProvider()
		assert.Nil(t, provider)
	})

	t.Run("initializes LoggerProvider when DD_LOGS_OTEL_ENABLED=true", func(t *testing.T) {
		// Clean up any existing provider
		_ = ShutdownGlobalLoggerProvider(context.Background())

		t.Setenv("DD_LOGS_OTEL_ENABLED", "true")
		config.SetUseFreshConfig(true)
		defer config.SetUseFreshConfig(false)

		err := Start(context.Background())
		require.NoError(t, err)

		// Provider should be initialized
		provider := GetGlobalLoggerProvider()
		assert.NotNil(t, provider)

		// Clean up
		err = Stop()
		assert.NoError(t, err)
	})

	t.Run("is idempotent", func(t *testing.T) {
		// Clean up any existing provider
		_ = ShutdownGlobalLoggerProvider(context.Background())

		t.Setenv("DD_LOGS_OTEL_ENABLED", "true")
		config.SetUseFreshConfig(true)
		defer config.SetUseFreshConfig(false)

		err1 := Start(context.Background())
		require.NoError(t, err1)

		provider1 := GetGlobalLoggerProvider()
		require.NotNil(t, provider1)

		// Call again
		err2 := Start(context.Background())
		require.NoError(t, err2)

		provider2 := GetGlobalLoggerProvider()
		require.NotNil(t, provider2)

		// Should be the same instance
		assert.Same(t, provider1, provider2)

		// Clean up
		err := Stop()
		assert.NoError(t, err)
	})
}

func TestStop(t *testing.T) {
	t.Run("does nothing when provider not initialized", func(t *testing.T) {
		// Ensure no provider exists
		_ = ShutdownGlobalLoggerProvider(context.Background())

		// Should not panic and should return no error
		err := Stop()
		assert.NoError(t, err)

		// Provider should still be nil
		provider := GetGlobalLoggerProvider()
		assert.Nil(t, provider)
	})

	t.Run("shuts down initialized provider", func(t *testing.T) {
		// Initialize provider
		err := InitGlobalLoggerProvider(context.Background())
		require.NoError(t, err)

		provider := GetGlobalLoggerProvider()
		require.NotNil(t, provider)

		// Stop
		err = Stop()
		assert.NoError(t, err)

		// Provider should be nil after stop
		provider = GetGlobalLoggerProvider()
		assert.Nil(t, provider)
	})

	t.Run("is idempotent", func(t *testing.T) {
		// Initialize provider
		err := InitGlobalLoggerProvider(context.Background())
		require.NoError(t, err)

		// Stop multiple times
		err = Stop()
		assert.NoError(t, err)
		err = Stop()
		assert.NoError(t, err)
		err = Stop()
		assert.NoError(t, err)

		// Provider should be nil
		provider := GetGlobalLoggerProvider()
		assert.Nil(t, provider)
	})
}

func TestIntegration(t *testing.T) {
	t.Run("full lifecycle with DD_LOGS_OTEL_ENABLED=true", func(t *testing.T) {
		// Clean up any existing provider
		_ = ShutdownGlobalLoggerProvider(context.Background())

		t.Setenv("DD_LOGS_OTEL_ENABLED", "true")
		t.Setenv("DD_SERVICE", "test-service")
		t.Setenv("DD_ENV", "test")
		t.Setenv("DD_VERSION", "1.0.0")
		config.SetUseFreshConfig(true)
		defer config.SetUseFreshConfig(false)

		// Start
		err := Start(context.Background())
		require.NoError(t, err)

		provider := GetGlobalLoggerProvider()
		require.NotNil(t, provider)

		// Stop
		err = Stop()
		assert.NoError(t, err)

		provider = GetGlobalLoggerProvider()
		assert.Nil(t, provider)
	})

	t.Run("full lifecycle with DD_LOGS_OTEL_ENABLED=false", func(t *testing.T) {
		// Clean up any existing provider
		_ = ShutdownGlobalLoggerProvider(context.Background())

		config.SetUseFreshConfig(true)
		defer config.SetUseFreshConfig(false)

		// Start (should be no-op)
		err := Start(context.Background())
		require.NoError(t, err)

		provider := GetGlobalLoggerProvider()
		assert.Nil(t, provider)

		// Stop (should be no-op)
		err = Stop()
		assert.NoError(t, err)

		provider = GetGlobalLoggerProvider()
		assert.Nil(t, provider)
	})
}

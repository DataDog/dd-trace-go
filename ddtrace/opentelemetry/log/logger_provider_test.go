// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitGlobalLoggerProvider(t *testing.T) {
	t.Run("creates LoggerProvider", func(t *testing.T) {
		// Clean up any existing provider
		_ = ShutdownGlobalLoggerProvider(context.Background())

		err := InitGlobalLoggerProvider(context.Background())
		require.NoError(t, err)

		provider := GetGlobalLoggerProvider()
		assert.NotNil(t, provider)

		// Clean up
		err = ShutdownGlobalLoggerProvider(context.Background())
		assert.NoError(t, err)
	})

	t.Run("is idempotent", func(t *testing.T) {
		// Clean up any existing provider
		_ = ShutdownGlobalLoggerProvider(context.Background())

		err1 := InitGlobalLoggerProvider(context.Background())
		require.NoError(t, err1)

		provider1 := GetGlobalLoggerProvider()
		require.NotNil(t, provider1)

		// Call again
		err2 := InitGlobalLoggerProvider(context.Background())
		require.NoError(t, err2)

		provider2 := GetGlobalLoggerProvider()
		require.NotNil(t, provider2)

		// Should be the same instance
		assert.Same(t, provider1, provider2)

		// Clean up
		err := ShutdownGlobalLoggerProvider(context.Background())
		assert.NoError(t, err)
	})

	t.Run("respects DD_SERVICE", func(t *testing.T) {
		// Clean up any existing provider
		_ = ShutdownGlobalLoggerProvider(context.Background())

		t.Setenv("DD_SERVICE", "test-service")

		err := InitGlobalLoggerProvider(context.Background())
		require.NoError(t, err)

		provider := GetGlobalLoggerProvider()
		assert.NotNil(t, provider)

		// Clean up
		err = ShutdownGlobalLoggerProvider(context.Background())
		assert.NoError(t, err)
	})

	t.Run("respects BLRP environment variables", func(t *testing.T) {
		// Clean up any existing provider
		_ = ShutdownGlobalLoggerProvider(context.Background())

		t.Setenv("OTEL_BLRP_MAX_QUEUE_SIZE", "1024")
		t.Setenv("OTEL_BLRP_SCHEDULE_DELAY", "500")
		t.Setenv("OTEL_BLRP_EXPORT_TIMEOUT", "15000")
		t.Setenv("OTEL_BLRP_MAX_EXPORT_BATCH_SIZE", "256")

		err := InitGlobalLoggerProvider(context.Background())
		require.NoError(t, err)

		provider := GetGlobalLoggerProvider()
		assert.NotNil(t, provider)

		// Verify env vars were read
		assert.Equal(t, 1024, resolveBLRPMaxQueueSize())
		assert.Equal(t, 500*time.Millisecond, resolveBLRPScheduleDelay())
		assert.Equal(t, 15000*time.Millisecond, resolveBLRPExportTimeout())
		assert.Equal(t, 256, resolveBLRPMaxExportBatchSize())

		// Clean up
		err = ShutdownGlobalLoggerProvider(context.Background())
		assert.NoError(t, err)
	})
}

func TestShutdownGlobalLoggerProvider(t *testing.T) {
	t.Run("shuts down existing provider", func(t *testing.T) {
		// Initialize provider
		err := InitGlobalLoggerProvider(context.Background())
		require.NoError(t, err)

		provider := GetGlobalLoggerProvider()
		require.NotNil(t, provider)

		// Shutdown
		err = ShutdownGlobalLoggerProvider(context.Background())
		assert.NoError(t, err)

		// Provider should be nil after shutdown
		provider = GetGlobalLoggerProvider()
		assert.Nil(t, provider)
	})

	t.Run("is idempotent when no provider exists", func(t *testing.T) {
		// Ensure no provider exists
		_ = ShutdownGlobalLoggerProvider(context.Background())

		// Shutdown again
		err := ShutdownGlobalLoggerProvider(context.Background())
		assert.NoError(t, err)
	})

	t.Run("respects context timeout", func(t *testing.T) {
		// Initialize provider
		err := InitGlobalLoggerProvider(context.Background())
		require.NoError(t, err)

		// Shutdown with very short timeout
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
		defer cancel()

		// Sleep a bit to ensure context expires
		time.Sleep(10 * time.Millisecond)

		err = ShutdownGlobalLoggerProvider(ctx)
		// May or may not error depending on timing, but should not panic
		// The important thing is it completes

		// Provider should still be cleaned up
		provider := GetGlobalLoggerProvider()
		assert.Nil(t, provider)
	})

	t.Run("allows reinitialization after shutdown", func(t *testing.T) {
		// Initialize
		err := InitGlobalLoggerProvider(context.Background())
		require.NoError(t, err)

		provider1 := GetGlobalLoggerProvider()
		require.NotNil(t, provider1)

		// Shutdown
		err = ShutdownGlobalLoggerProvider(context.Background())
		assert.NoError(t, err)

		// Reinitialize
		err = InitGlobalLoggerProvider(context.Background())
		require.NoError(t, err)

		provider2 := GetGlobalLoggerProvider()
		require.NotNil(t, provider2)

		// Should be a different instance
		assert.NotSame(t, provider1, provider2)

		// Clean up
		err = ShutdownGlobalLoggerProvider(context.Background())
		assert.NoError(t, err)
	})
}

func TestGetGlobalLoggerProvider(t *testing.T) {
	t.Run("returns nil when not initialized", func(t *testing.T) {
		// Ensure no provider exists
		_ = ShutdownGlobalLoggerProvider(context.Background())

		provider := GetGlobalLoggerProvider()
		assert.Nil(t, provider)
	})

	t.Run("returns provider when initialized", func(t *testing.T) {
		// Clean up any existing provider
		_ = ShutdownGlobalLoggerProvider(context.Background())

		err := InitGlobalLoggerProvider(context.Background())
		require.NoError(t, err)

		provider := GetGlobalLoggerProvider()
		assert.NotNil(t, provider)

		// Clean up
		err = ShutdownGlobalLoggerProvider(context.Background())
		assert.NoError(t, err)
	})
}

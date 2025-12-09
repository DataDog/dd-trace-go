// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGet(t *testing.T) {
	t.Run("returns non-nil config", func(t *testing.T) {
		resetGlobalState()
		defer resetGlobalState()

		cfg := Get()
		assert.NotNil(t, cfg, "Get() should never return nil")
	})

	t.Run("singleton behavior - returns same instance", func(t *testing.T) {
		resetGlobalState()
		defer resetGlobalState()

		cfg1 := Get()
		cfg2 := Get()
		cfg3 := Get()

		// All calls should return the same instance
		assert.Same(t, cfg1, cfg2, "First and second Get() calls should return the same instance")
		assert.Same(t, cfg1, cfg3, "First and third Get() calls should return the same instance")
	})

	t.Run("fresh config flag forces new instance", func(t *testing.T) {
		resetGlobalState()
		defer resetGlobalState()

		// Get the first instance
		cfg1 := Get()
		require.NotNil(t, cfg1)

		// Enable fresh config to allow us to create new instances
		SetUseFreshConfig(true)

		// Get should now return a new instance
		cfg2 := Get()
		require.NotNil(t, cfg2)
		assert.NotSame(t, cfg1, cfg2, "With useFreshConfig=true, Get() should return a new instance")

		// Another call with useFreshConfig still true should return another new instance
		cfg3 := Get()
		require.NotNil(t, cfg3)
		assert.NotSame(t, cfg2, cfg3, "With useFreshConfig=true, each Get() call should return a new instance")

		// Disable fresh config to allow us to cache the same instance
		SetUseFreshConfig(false)

		// Now it should cache the same instance
		cfg4 := Get()
		cfg5 := Get()
		assert.Same(t, cfg4, cfg5, "With useFreshConfig=false, Get() should cache the same instance")
	})

	t.Run("concurrent access is safe", func(t *testing.T) {
		resetGlobalState()
		defer resetGlobalState()

		const numGoroutines = 100
		var wg sync.WaitGroup
		wg.Add(numGoroutines)

		// All goroutines should get a non-nil config
		configs := make([]*Config, numGoroutines)

		for i := range numGoroutines {
			go func(j int) {
				defer wg.Done()
				configs[j] = Get()
			}(i)
		}

		wg.Wait()

		// All configs should be non-nil
		for i, cfg := range configs {
			assert.NotNil(t, cfg, "Config at index %d should not be nil", i)
		}

		// All configs should be the same instance (singleton)
		firstConfig := configs[0]
		for i, cfg := range configs[1:] {
			assert.Same(t, firstConfig, cfg, "Config at index %d should be the same instance", i+1)
		}
	})

	t.Run("concurrent access with fresh config", func(t *testing.T) {
		resetGlobalState()
		defer resetGlobalState()

		// Enable fresh config to allow us to create new instances
		SetUseFreshConfig(true)

		const numGoroutines = 50
		var wg sync.WaitGroup
		wg.Add(numGoroutines)

		// Track if we get different instances (which is expected with useFreshConfig=true)
		var uniqueInstances sync.Map
		var configCount atomic.Int32

		for range numGoroutines {
			go func() {
				defer wg.Done()
				cfg := Get()
				require.NotNil(t, cfg, "Get() should not return nil even under concurrent access")

				// Track unique instances
				if _, loaded := uniqueInstances.LoadOrStore(cfg, true); !loaded {
					configCount.Add(1)
				}
			}()
		}

		wg.Wait()

		// With useFreshConfig=true, we should get multiple different instances
		count := configCount.Load()
		assert.Greater(t, count, int32(1), "With useFreshConfig=true, should get multiple different instances")
	})

	t.Run("config is properly initialized with values", func(t *testing.T) {
		resetGlobalState()
		defer resetGlobalState()

		// Set an environment variable to ensure it's loaded
		t.Setenv("DD_TRACE_DEBUG", "true")

		cfg := Get()
		require.NotNil(t, cfg)

		// Verify that config values are accessible (using the Debug() method)
		debug := cfg.Debug()
		assert.True(t, debug, "Config should have loaded DD_TRACE_DEBUG=true")
	})

	t.Run("Setter methods update config and maintain thread-safety", func(t *testing.T) {
		resetGlobalState()
		defer resetGlobalState()

		cfg := Get()
		require.NotNil(t, cfg)

		initialDebug := cfg.Debug()
		cfg.SetDebug(!initialDebug, "test")
		assert.Equal(t, !initialDebug, cfg.Debug(), "Debug setting should have changed")

		// Verify concurrent reads don't panic
		const numReaders = 100
		var wg sync.WaitGroup
		wg.Add(numReaders)

		for range numReaders {
			go func() {
				defer wg.Done()
				_ = cfg.Debug()
			}()
		}

		wg.Wait()
	})

	t.Run("SetDebug concurrent with reads is safe", func(t *testing.T) {
		resetGlobalState()
		defer resetGlobalState()

		cfg := Get()
		require.NotNil(t, cfg)

		var wg sync.WaitGroup
		const numOperations = 100

		// Start readers
		wg.Add(numOperations)
		for range numOperations {
			go func() {
				defer wg.Done()
				_ = cfg.Debug()
			}()
		}

		// Start writers
		wg.Add(numOperations)
		for i := range numOperations {
			go func(val bool) {
				defer wg.Done()
				cfg.SetDebug(val, "test")
			}(i%2 == 0)
		}

		wg.Wait()

		// Should not panic and should have a valid boolean value
		finalDebug := cfg.Debug()
		assert.IsType(t, true, finalDebug)
	})
}

// resetGlobalState resets all global singleton state for testing
func resetGlobalState() {
	mu = sync.Mutex{}
	instance = nil
	useFreshConfig = false
}

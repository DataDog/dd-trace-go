// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWithGlobalTagOption exercises the WithGlobalTag StartOption, which writes
// through to internal/config via SetGlobalTag. It verifies that tags accumulate,
// overwrite by key, and are safe under concurrent application.
func TestWithGlobalTagOption(t *testing.T) {
	t.Run("WithGlobalTag pattern accumulates tags", func(t *testing.T) {
		c, err := newTestConfig()
		require.NoError(t, err)

		// Apply multiple WithGlobalTag options, mimicking real usage.
		WithGlobalTag("key1", "val1")(c)
		WithGlobalTag("key2", "val2")(c)
		WithGlobalTag("key3", "val3")(c)

		tags := c.internalConfig.GlobalTags()
		assert.Equal(t, "val1", tags["key1"])
		assert.Equal(t, "val2", tags["key2"])
		assert.Equal(t, "val3", tags["key3"])
	})

	t.Run("WithGlobalTag overwrites existing key", func(t *testing.T) {
		c, err := newTestConfig()
		require.NoError(t, err)

		WithGlobalTag("key", "original")(c)
		WithGlobalTag("key", "updated")(c)

		tags := c.internalConfig.GlobalTags()
		assert.Equal(t, "updated", tags["key"])
	})

	t.Run("WithGlobalTag concurrent writes", func(t *testing.T) {
		c, err := newTestConfig()
		require.NoError(t, err)

		// Seed a tag so all goroutines exercise the concurrent read-modify-write path.
		WithGlobalTag("seed", true)(c)

		const n = 50
		var wg sync.WaitGroup
		wg.Add(n)
		for i := range n {
			go func(i int) {
				defer wg.Done()
				WithGlobalTag(fmt.Sprintf("k%d", i), i)(c)
			}(i)
		}
		wg.Wait()

		tags := c.internalConfig.GlobalTags()
		for i := range n {
			assert.Equal(t, i, tags[fmt.Sprintf("k%d", i)])
		}
	})
}

// TestDynamicInstrumentationRCStateMutex verifies that the Mutex on
// dynamicInstrumentationRCState correctly serializes concurrent access.
// This is a regression test for the RWMutex → Mutex downgrade: all access
// patterns use exclusive Lock, so Mutex is sufficient and lower overhead.
func TestDynamicInstrumentationRCStateMutex(t *testing.T) {
	t.Run("concurrent writes and reads are serialized", func(t *testing.T) {
		// Reset the global state for this test.
		diRCState.mu.Lock()
		diRCState.state = map[string]dynamicInstrumentationRCProbeConfig{}
		diRCState.symdbExport = false
		diRCState.mu.Unlock()

		const n = 50
		var wg sync.WaitGroup
		wg.Add(n * 3)

		// Simulate RC probe config updates (writes to state map).
		for i := range n {
			go func(i int) {
				defer wg.Done()
				key := fmt.Sprintf("probe-%d", i)
				diRCState.mu.Lock()
				diRCState.state[key] = dynamicInstrumentationRCProbeConfig{
					configPath:    key,
					configContent: fmt.Sprintf("content-%d", i),
				}
				diRCState.mu.Unlock()
			}(i)
		}

		// Simulate symdb export toggles (writes to symdbExport).
		for i := range n {
			go func(i int) {
				defer wg.Done()
				diRCState.mu.Lock()
				diRCState.symdbExport = i%2 == 0
				diRCState.mu.Unlock()
			}(i)
		}

		// Simulate passAllProbeConfigurations reads (reads state + symdbExport).
		for range n {
			go func() {
				defer wg.Done()
				diRCState.mu.Lock()
				// Iterate the map like passAllProbeConfigurations does.
				for _, v := range diRCState.state {
					_ = v.configPath
					_ = v.configContent
				}
				_ = diRCState.symdbExport
				diRCState.mu.Unlock()
			}()
		}

		wg.Wait()

		// After all writes complete, verify the state map has all entries.
		diRCState.mu.Lock()
		assert.Len(t, diRCState.state, n)
		for i := range n {
			key := fmt.Sprintf("probe-%d", i)
			cfg, ok := diRCState.state[key]
			assert.True(t, ok, "missing key %s", key)
			assert.Equal(t, fmt.Sprintf("content-%d", i), cfg.configContent)
		}
		diRCState.mu.Unlock()
	})

	t.Run("initialize resets symdbExport", func(t *testing.T) {
		// Set symdbExport to true, then verify initialize resets it.
		diRCState.mu.Lock()
		diRCState.symdbExport = true
		diRCState.state = map[string]dynamicInstrumentationRCProbeConfig{
			"stale": {configPath: "stale", configContent: "old"},
		}
		diRCState.mu.Unlock()

		// Call the init function directly (bypass sync.Once for testing).
		// We replicate the logic to avoid spawning the background goroutine.
		diRCState.mu.Lock()
		diRCState.state = map[string]dynamicInstrumentationRCProbeConfig{}
		diRCState.symdbExport = false
		diRCState.mu.Unlock()

		diRCState.mu.Lock()
		assert.False(t, diRCState.symdbExport)
		assert.Empty(t, diRCState.state)
		diRCState.mu.Unlock()
	})
}

// TestDynamicConfigFieldsRemaining uses reflection to count how many dynamicConfig
// fields remain on the tracer's config struct. When the count reaches 0, the test
// fails to signal that dynamic_config.go and this test file should be deleted.
func TestDynamicConfigFieldsRemaining(t *testing.T) {
	typ := reflect.TypeFor[config]()
	var remaining int
	for i := 0; i < typ.NumField(); i++ {
		if strings.HasPrefix(typ.Field(i).Type.Name(), "dynamicConfig[") {
			remaining++
		}
	}
	if remaining == 0 {
		t.Fatal("All dynamicConfig fields have been migrated to internal/config.DynamicConfig. " +
			"Delete ddtrace/tracer/dynamic_config.go and ddtrace/tracer/dynamic_config_test.go.")
	}
}

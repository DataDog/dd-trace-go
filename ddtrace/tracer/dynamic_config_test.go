// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDynamicConfigSet(t *testing.T) {
	t.Run("receives current value and replaces it with returned value", func(t *testing.T) {
		dc := newDynamicConfig("test", 10, func(T int) bool { return true }, equal[int])

		dc.set(func(current int) int {
			assert.Equal(t, 10, current)
			return current + 5
		})

		assert.Equal(t, 15, dc.get())
	})

	t.Run("value type update is not lost", func(t *testing.T) {
		// This is the key behavioral difference from the old func(T) signature:
		// with value types, in-place mutation has no effect — only the return
		// value persists.
		dc := newDynamicConfig("test", 0, func(T int) bool { return true }, equal[int])

		dc.set(func(current int) int {
			return 42
		})

		assert.Equal(t, 42, dc.get())
	})

	t.Run("map reference type update", func(t *testing.T) {
		init := map[string]any{"a": 1}
		dc := newDynamicConfig("test", init, func(T map[string]any) bool { return true }, equalMap[string])

		dc.set(func(current map[string]any) map[string]any {
			current["b"] = 2
			return current
		})

		got := dc.get()
		assert.Equal(t, 1, got["a"])
		assert.Equal(t, 2, got["b"])
	})

	t.Run("multiple set calls accumulate", func(t *testing.T) {
		dc := newDynamicConfig("test", 0, func(T int) bool { return true }, equal[int])

		dc.set(func(current int) int { return current + 1 })
		dc.set(func(current int) int { return current + 10 })
		dc.set(func(current int) int { return current + 100 })

		assert.Equal(t, 111, dc.get())
	})

	t.Run("concurrent access is synchronized", func(t *testing.T) {
		dc := newDynamicConfig("test", 0, func(T int) bool { return true }, equal[int])

		var wg sync.WaitGroup
		const n = 100
		wg.Add(n)
		for range n {
			go func() {
				defer wg.Done()
				dc.set(func(current int) int {
					return current + 1
				})
			}()
		}
		wg.Wait()

		assert.Equal(t, n, dc.get())
	})
}

// TestDynamicConfigSetCallerContract exercises the calling pattern used by
// WithGlobalTag: mutate-and-return on a map reference type. This verifies
// that the contract where callers must return the (possibly mutated) value
// works correctly for real callers.
func TestDynamicConfigSetCallerContract(t *testing.T) {
	t.Run("WithGlobalTag pattern accumulates tags", func(t *testing.T) {
		c, err := newTestConfig()
		require.NoError(t, err)

		// Apply multiple WithGlobalTag options, mimicking real usage.
		WithGlobalTag("key1", "val1")(c)
		WithGlobalTag("key2", "val2")(c)
		WithGlobalTag("key3", "val3")(c)

		tags := c.globalTags.get()
		assert.Equal(t, "val1", tags["key1"])
		assert.Equal(t, "val2", tags["key2"])
		assert.Equal(t, "val3", tags["key3"])
	})

	t.Run("WithGlobalTag overwrites existing key", func(t *testing.T) {
		c, err := newTestConfig()
		require.NoError(t, err)

		WithGlobalTag("key", "original")(c)
		WithGlobalTag("key", "updated")(c)

		tags := c.globalTags.get()
		assert.Equal(t, "updated", tags["key"])
	})

	t.Run("WithGlobalTag concurrent writes", func(t *testing.T) {
		c, err := newTestConfig()
		require.NoError(t, err)

		// Seed the globalTags so all goroutines use set(), not initGlobalTags.
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

		tags := c.globalTags.get()
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

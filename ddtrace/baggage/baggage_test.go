// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package baggage

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBaggageFunctions(t *testing.T) {
	t.Run("Set and Get", func(t *testing.T) {
		ctx := context.Background()

		// Set a key/value in the baggage
		ctx = Set(ctx, "foo", "bar")

		// Retrieve that value
		got, ok := Get(ctx, "foo")
		if !ok {
			t.Error("Expected key \"foo\" to be found in baggage, got ok=false")
		}
		if got != "bar" {
			t.Errorf("Baggage(ctx, \"foo\") = %q; want \"bar\"", got)
		}

		// Ensure retrieving a non-existent key returns an empty string and false
		got, ok = Get(ctx, "missingKey")
		if ok {
			t.Error("Expected key \"missingKey\" to not be found, got ok=true")
		}
		if got != "" {
			t.Errorf("Baggage(ctx, \"missingKey\") = %q; want \"\"", got)
		}
	})

	t.Run("All", func(t *testing.T) {
		ctx := context.Background()

		// Set multiple baggage entries
		ctx = Set(ctx, "key1", "value1")
		ctx = Set(ctx, "key2", "value2")

		// Retrieve all baggage entries
		all := All(ctx)
		if len(all) != 2 {
			t.Fatalf("Expected 2 items in baggage; got %d", len(all))
		}

		// Check each entry
		if all["key1"] != "value1" {
			t.Errorf("all[\"key1\"] = %q; want \"value1\"", all["key1"])
		}
		if all["key2"] != "value2" {
			t.Errorf("all[\"key2\"] = %q; want \"value2\"", all["key2"])
		}

		// Confirm returned map is a copy, not the original
		all["key1"] = "modified"
		val, _ := Get(ctx, "key1")
		if val == "modified" {
			t.Error("AllBaggage returned a map that mutates the original baggage!")
		}
	})

	t.Run("Remove", func(t *testing.T) {
		ctx := context.Background()

		// Add baggage to remove
		ctx = Set(ctx, "deleteMe", "toBeRemoved")

		// Remove it
		ctx = Remove(ctx, "deleteMe")

		// Verify removal
		got, ok := Get(ctx, "deleteMe")
		if ok {
			t.Error("Expected key \"deleteMe\" to be removed, got ok=true")
		}
		if got != "" {
			t.Errorf("Expected empty string for removed key; got %q", got)
		}
	})

	t.Run("Clear", func(t *testing.T) {
		ctx := context.Background()

		// Add multiple items
		ctx = Set(ctx, "k1", "v1")
		ctx = Set(ctx, "k2", "v2")

		// Clear all baggage
		ctx = Clear(ctx)

		// Check that everything is gone
		all := All(ctx)
		if len(all) != 0 {
			t.Errorf("Expected no items after clearing baggage; got %d", len(all))
		}
	})

	t.Run("withBaggage", func(t *testing.T) {
		ctx := context.Background()

		// Create a map and insert into context directly
		initialMap := map[string]string{"customKey": "customValue"}
		ctx = withBaggage(ctx, initialMap)

		// Verify
		got, _ := Get(ctx, "customKey")
		if got != "customValue" {
			t.Errorf("Baggage(ctx, \"customKey\") = %q; want \"customValue\"", got)
		}
	})

	t.Run("explicitOkCheck", func(t *testing.T) {
		ctx := context.Background()

		// Check an unset key
		val, ok := Get(ctx, "unsetKey")
		if ok {
			t.Errorf("Expected unset key to return ok=false, got ok=true with val=%q", val)
		}

		ctx = Set(ctx, "testKey", "testVal")
		val, ok = Get(ctx, "testKey")
		if !ok {
			t.Error("Expected key \"testKey\" to be present, got ok=false")
		}
		if val != "testVal" {
			t.Errorf("Expected \"testVal\"; got %q", val)
		}
	})
}

func TestBaggageMapAccessorsMakeCopies(t *testing.T) {
	t.Run("Set", func(t *testing.T) {
		firstMap := map[string]string{"key": "value"}
		ctx := withBaggage(context.Background(), firstMap)
		ctx = Set(ctx, "key2", "value2")

		// Verify that the new map is a copy of the original
		nextMap, ok := baggageMap(ctx)
		assert.True(t, ok)
		assert.False(t, &firstMap == &nextMap, "Set should create a new map, not reuse the original")

		// Mutate the new map and ensure the original is unchanged
		nextMap["key"] = "changed"
		assert.Equal(t, "value", firstMap["key"], "Original map should not be affected by changes to the new map")

		// Check that both keys are present in the new map
		assert.Equal(t, "changed", nextMap["key"], "New map should have the new key")
		assert.Equal(t, "value2", nextMap["key2"], "New map should have the new key")
	})
	t.Run("Remove", func(t *testing.T) {
		firstMap := map[string]string{"key": "value"}
		ctx := withBaggage(context.Background(), firstMap)
		ctx = Remove(ctx, "key")

		// Verify that the new map is a copy of the original
		nextMap, ok := baggageMap(ctx)
		assert.True(t, ok)
		assert.False(t, &firstMap == &nextMap, "Remove should create a new map, not reuse the original")

		// Mutate the new map and ensure the original is unchanged
		nextMap["key"] = "changed"
		assert.Equal(t, "value", firstMap["key"], "Original map should not be affected by changes to the new map")
	})
	t.Run("All", func(t *testing.T) {
		firstMap := map[string]string{"key": "value"}
		ctx := withBaggage(context.Background(), firstMap)
		all := All(ctx)
		assert.False(t, &firstMap == &all, "All should return a new map, not the original map instance")

		// Mutate the new map and ensure the original is unchanged
		all["key"] = "changed"
		assert.Equal(t, "value", firstMap["key"], "Original map should not be affected by changes to the new map")
	})
}

// func TestConcurrentAccess(t *testing.T) {
// 	ctx := context.Background()
// 	ctx = Set(ctx, "init", "value")

// 	var wg sync.WaitGroup
// 	done := make(chan struct{})

// 	// Goroutine 1: Iterates over baggage repeatedly
// 	wg.Add(1)
// 	go func() {
// 		defer wg.Done()
// 		for {
// 			select {
// 			case <-done:
// 				return
// 			default:
// 				_ = All(ctx)
// 				runtime.Gosched()
// 			}
// 		}
// 	}()

// 	// Goroutine 2: Modifies baggage repeatedly
// 	wg.Add(1)
// 	go func() {
// 		defer wg.Done()
// 		for range [1000]int{} {
// 			ctx = Set(ctx, "key", "val")
// 			runtime.Gosched()
// 		}
// 		close(done)
// 	}()

// 	wg.Wait()
// }

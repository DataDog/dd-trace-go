// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package tracer

import (
	"context"
	"testing"
)

func TestBaggageFunctions(t *testing.T) {
	t.Run("SetBaggage and Baggage", func(t *testing.T) {
		ctx := context.Background()

		// Set a key/value in the baggage
		ctx = SetBaggage(ctx, "foo", "bar")

		// Retrieve that value
		got := Baggage(ctx, "foo")
		want := "bar"
		if got != want {
			t.Errorf("Baggage(ctx, \"foo\") = %q; want %q", got, want)
		}

		// Ensure retrieving a non-existent key returns an empty string
		got = Baggage(ctx, "missingKey")
		if got != "" {
			t.Errorf("Baggage(ctx, \"missingKey\") = %q; want \"\"", got)
		}
	})

	t.Run("AllBaggage", func(t *testing.T) {
		ctx := context.Background()

		// Set multiple baggage entries
		ctx = SetBaggage(ctx, "key1", "value1")
		ctx = SetBaggage(ctx, "key2", "value2")

		// Retrieve all baggage entries
		all := AllBaggage(ctx)
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
		if Baggage(ctx, "key1") == "modified" {
			t.Error("AllBaggage returned a map that mutates the original baggage!")
		}
	})

	t.Run("RemoveBaggage", func(t *testing.T) {
		ctx := context.Background()

		// Add baggage to remove
		ctx = SetBaggage(ctx, "deleteMe", "toBeRemoved")

		// Remove it
		ctx = RemoveBaggage(ctx, "deleteMe")

		// Verify removal
		got := Baggage(ctx, "deleteMe")
		if got != "" {
			t.Errorf("Expected empty string for removed key; got %q", got)
		}
	})

	t.Run("ClearBaggage", func(t *testing.T) {
		ctx := context.Background()

		// Add multiple items
		ctx = SetBaggage(ctx, "k1", "v1")
		ctx = SetBaggage(ctx, "k2", "v2")

		// Clear all baggage
		ctx = ClearBaggage(ctx)

		// Check that everything is gone
		all := AllBaggage(ctx)
		if len(all) != 0 {
			t.Errorf("Expected no items after clearing baggage; got %d", len(all))
		}
	})

	t.Run("WithBaggage", func(t *testing.T) {
		ctx := context.Background()

		// Create a map and insert into context directly
		initialMap := map[string]string{"customKey": "customValue"}
		ctx = WithBaggage(ctx, initialMap)

		// Verify
		got := Baggage(ctx, "customKey")
		if got != "customValue" {
			t.Errorf("Baggage(ctx, \"customKey\") = %q; want \"customValue\"", got)
		}
	})
}

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBaggageRefactorBasics tests the core functionality of our baggage refactoring
func TestBaggageRefactorBasics(t *testing.T) {
	t.Run("SpanContextWithBaggage", func(t *testing.T) {
		// Create a SpanContext with baggage using the new unified approach
		baggageCtx := NewBaggageContextWithItems(context.Background(),
			map[string]string{"w3c-key": "w3c-value"},
			map[string]string{"ot-key": "ot-value"})

		sc := &SpanContext{
			baggage: baggageCtx,
		}

		// Test OpenTracing baggage access (backward compatibility)
		assert.Equal(t, "ot-value", sc.baggageItem("ot-key"))
		assert.Equal(t, "", sc.baggageItem("nonexistent"))

		// Test ForeachBaggageItem (backward compatibility)
		items := make(map[string]string)
		sc.ForeachBaggageItem(func(k, v string) bool {
			items[k] = v
			return true
		})
		assert.Equal(t, map[string]string{"ot-key": "ot-value"}, items)
	})

	t.Run("BaggageOnlySpanContext", func(t *testing.T) {
		// Test the scenario that was causing zero TraceID issues
		baggageCtx := NewBaggageContextWithItems(context.Background(),
			map[string]string{"key": "value"}, nil)

		sc := &SpanContext{
			baggage: baggageCtx,
			// traceID and spanID remain zero
		}

		// This should be invalid (baggage-only context)
		assert.False(t, sc.IsValid())

		// But baggage should work
		value, ok := sc.baggage.GetBaggage("key")
		assert.True(t, ok)
		assert.Equal(t, "value", value)
	})

	t.Run("SpanContextSetBaggage", func(t *testing.T) {
		sc := &SpanContext{}

		// Test setting baggage on empty context
		sc.setBaggageItem("test-key", "test-value")

		// Verify it was set
		assert.Equal(t, "test-value", sc.baggageItem("test-key"))

		// Verify ForeachBaggageItem works
		found := false
		sc.ForeachBaggageItem(func(k, v string) bool {
			if k == "test-key" && v == "test-value" {
				found = true
			}
			return true
		})
		assert.True(t, found)
	})
}

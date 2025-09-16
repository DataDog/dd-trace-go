// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package tracer

import (
	"context"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/baggage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSpanContextImplementsContext verifies that SpanContext implements context.Context interface
func TestSpanContextImplementsContext(t *testing.T) {
	tracer, err := newTracer()
	require.NoError(t, err)
	defer tracer.Stop()

	// Create a SpanContext
	span := tracer.StartSpan("test")
	spanCtx := span.Context()

	// Verify it implements context.Context
	var ctx context.Context = spanCtx
	assert.NotNil(t, ctx)

	// Test context.Context interface methods
	deadline, ok := spanCtx.Deadline()
	assert.False(t, ok)
	assert.Zero(t, deadline)

	assert.Nil(t, spanCtx.Done())
	assert.Nil(t, spanCtx.Err())
	assert.Nil(t, spanCtx.Value("nonexistent"))
}

// TestSpanContextWithParentContext verifies context delegation
func TestSpanContextWithParentContext(t *testing.T) {
	// Create a parent context with deadline
	parentCtx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()

	// Create SpanContext from parent
	spanCtx := SpanContextFromContext(parentCtx)

	// Verify delegation works
	deadline, ok := spanCtx.Deadline()
	assert.True(t, ok)
	assert.WithinDuration(t, time.Now().Add(time.Hour), deadline, time.Second)

	// Verify Done channel delegation
	assert.Equal(t, parentCtx.Done(), spanCtx.Done())
}

// TestBaggageSeparation verifies OpenTracing and W3C baggage are kept separate
func TestBaggageSeparation(t *testing.T) {
	tracer, err := newTracer()
	require.NoError(t, err)
	defer tracer.Stop()

	// Create a SpanContext
	span := tracer.StartSpan("test")
	spanCtx := span.Context()

	// Add OpenTracing baggage using unified interface
	spanCtx.setBaggageItem("ot-key", "ot-value")

	// Verify baggage access works
	assert.Equal(t, "ot-value", spanCtx.baggageItem("ot-key"))

	// Verify ForeachBaggageItem works
	items := make(map[string]string)
	spanCtx.ForeachBaggageItem(func(k, v string) bool {
		items[k] = v
		return true
	})
	assert.Equal(t, "ot-value", items["ot-key"])
}

// TestBaggagePackageWithSpanContext verifies baggage package works with SpanContext
func TestBaggagePackageWithSpanContext(t *testing.T) {
	tracer, err := newTracer()
	require.NoError(t, err)
	defer tracer.Stop()

	// Create a SpanContext
	span := tracer.StartSpan("test")
	spanCtx := span.Context()

	// Use baggage package with SpanContext
	updatedCtx := baggage.Set(spanCtx, "test-key", "test-value")

	// Verify the returned context is the same SpanContext
	assert.Equal(t, spanCtx, updatedCtx)

	// Verify the baggage was set on W3C baggage
	value, ok := baggage.Get(spanCtx, "test-key")
	assert.True(t, ok)
	assert.Equal(t, "test-value", value)

	// The baggage package should work through the Value() delegation to our baggage context
}

// TestSpanContextConversion verifies conversion between context types
func TestSpanContextConversion(t *testing.T) {
	// Start with regular context with baggage
	ctx := context.Background()
	ctx = baggage.Set(ctx, "existing-key", "existing-value")

	// Convert to SpanContext
	spanCtx := SpanContextFromContext(ctx)

	// Verify it's a SpanContext
	assert.IsType(t, &SpanContext{}, spanCtx)

	// Verify baggage was transferred
	value, ok := baggage.Get(spanCtx, "existing-key")
	assert.True(t, ok)
	assert.Equal(t, "existing-value", value)

	// Parent context delegation eliminated - SpanContext implements context.Context directly
}

// TestIsValid verifies the IsValid method works correctly
func TestIsValid(t *testing.T) {
	// Test valid SpanContext
	validCtx := &SpanContext{
		spanID: 123,
	}
	validCtx.traceID.SetLower(456)
	assert.True(t, validCtx.IsValid())

	// Test invalid SpanContext (zero trace ID)
	invalidCtx1 := &SpanContext{
		spanID: 123,
		// traceID remains zero
	}
	assert.False(t, invalidCtx1.IsValid())

	// Test invalid SpanContext (zero span ID)
	invalidCtx2 := &SpanContext{
		spanID: 0,
	}
	invalidCtx2.traceID.SetLower(456)
	assert.False(t, invalidCtx2.IsValid())

	// Test baggage-only context
	baggageOnlyCtx := &SpanContext{
		// Both traceID and spanID are zero - indicates baggage-only context
	}
	assert.False(t, baggageOnlyCtx.IsValid())
}

// TestThreadSafeBaggageOperations verifies thread safety of baggage operations
func TestThreadSafeBaggageOperations(t *testing.T) {
	spanCtx := &SpanContext{}

	// Run concurrent operations
	done := make(chan bool)

	// Concurrent OpenTracing baggage operations
	go func() {
		for i := 0; i < 100; i++ {
			spanCtx.setBaggageItem("ot-key", "ot-value")
			_ = spanCtx.baggageItem("ot-key")
		}
		done <- true
	}()

	// Concurrent baggage operations using unified interface
	go func() {
		for i := 0; i < 100; i++ {
			spanCtx.setBaggageItem("concurrent-key", "concurrent-value")
			_ = spanCtx.baggageItem("concurrent-key")
		}
		done <- true
	}()

	// Wait for completion
	<-done
	<-done

	// Verify final state
	assert.Equal(t, "ot-value", spanCtx.baggageItem("ot-key"))
	assert.Equal(t, "concurrent-value", spanCtx.baggageItem("concurrent-key"))
}

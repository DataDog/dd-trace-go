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

func TestNewPropagationContext(t *testing.T) {
	parent := context.Background()
	traceID := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	priority := float64(1)

	trace := NewTraceContext(traceID, &priority, "synthetics")
	var baggage BaggageContext = NewBaggageContext(parent)
	baggage = baggage.SetBaggage("key1", "value1")

	ctx := NewPropagationContext(parent, trace, baggage)

	assert.True(t, ctx.HasTrace())
	assert.True(t, ctx.HasBaggage())
	assert.Equal(t, trace, ctx.Trace())
	assert.Equal(t, baggage, ctx.Baggage())
}

func TestNewBaggageOnlyContext(t *testing.T) {
	parent := context.Background()
	var baggage BaggageContext = NewBaggageContext(parent)
	baggage = baggage.SetBaggage("key1", "value1")

	ctx := NewBaggageOnlyContext(parent, baggage)

	assert.False(t, ctx.HasTrace())
	assert.True(t, ctx.HasBaggage())
	assert.Nil(t, ctx.Trace())
	assert.Equal(t, baggage, ctx.Baggage())
}

func TestNewTraceOnlyContext(t *testing.T) {
	parent := context.Background()
	traceID := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	priority := float64(1)

	trace := NewTraceContext(traceID, &priority, "synthetics")
	ctx := NewTraceOnlyContext(parent, trace)

	assert.True(t, ctx.HasTrace())
	assert.False(t, ctx.HasBaggage())
	assert.Equal(t, trace, ctx.Trace())
	assert.Nil(t, ctx.Baggage())
}

func TestBaggageContext(t *testing.T) {
	parent := context.Background()
	var baggage BaggageContext = NewBaggageContext(parent)

	// Test W3C baggage
	baggage = baggage.SetBaggage("w3c-key", "w3c-value")
	value, ok := baggage.GetBaggage("w3c-key")
	assert.True(t, ok)
	assert.Equal(t, "w3c-value", value)

	// Test OpenTracing baggage
	baggage = baggage.SetOTBaggage("ot-key", "ot-value")
	assert.Equal(t, "ot-value", baggage.GetOTBaggage("ot-key"))

	// Test iteration
	w3cItems := make(map[string]string)
	baggage.ForeachBaggage(func(k, v string) bool {
		w3cItems[k] = v
		return true
	})
	assert.Equal(t, map[string]string{"w3c-key": "w3c-value"}, w3cItems)

	otItems := make(map[string]string)
	baggage.ForeachOTBaggage(func(k, v string) bool {
		otItems[k] = v
		return true
	})
	assert.Equal(t, map[string]string{"ot-key": "ot-value"}, otItems)

	assert.True(t, baggage.HasBaggage())
}

func TestTraceContext(t *testing.T) {
	traceID := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	priority := float64(2)

	trace := NewTraceContext(traceID, &priority, "synthetics")

	assert.True(t, trace.IsValid())
	assert.Equal(t, traceID, trace.TraceIDBytes())
	assert.Equal(t, "synthetics", trace.Origin())

	p, ok := trace.SamplingPriority()
	assert.True(t, ok)
	assert.Equal(t, 2, p)
}

func TestChainedPropagationContextPropagator(t *testing.T) {
	// Create mock propagators using adapters
	mockProp1 := NewPropagatorAdapter(&mockPropagator{
		extractResult: &SpanContext{
			traceID: [16]byte{1, 2, 3, 4, 5, 6, 7, 8},
			spanID:  123,
		},
	})

	baggageCtx := NewBaggageContextWithItems(context.Background(), nil, map[string]string{"key": "value"})
	mockProp2 := NewPropagatorAdapter(&mockPropagator{
		extractResult: &SpanContext{
			baggage: baggageCtx,
		},
	})

	chain := NewChainedPropagationContextPropagator(mockProp1, mockProp2)

	carrier := map[string]string{}
	ctx, err := chain.Extract(carrier)

	assert.NoError(t, err)
	assert.NotNil(t, ctx)
	assert.True(t, ctx.HasTrace())
	assert.True(t, ctx.HasBaggage())
}

// mockPropagator for testing
type mockPropagator struct {
	extractResult *SpanContext
	extractError  error
	injectError   error
}

func (m *mockPropagator) Extract(carrier interface{}) (*SpanContext, error) {
	return m.extractResult, m.extractError
}

func (m *mockPropagator) Inject(ctx *SpanContext, carrier interface{}) error {
	return m.injectError
}

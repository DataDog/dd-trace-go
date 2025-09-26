// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package llmobs_test

import (
	"context"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/llmobs"
	"github.com/stretchr/testify/assert"
)

func TestContext(t *testing.T) {
	t.Run("active-llm-span-context", func(t *testing.T) {
		t.Run("empty-context", func(t *testing.T) {
			ctx := context.Background()
			span, ok := llmobs.ActiveLLMSpanFromContext(ctx)
			assert.False(t, ok)
			assert.Nil(t, span)
		})
		t.Run("with-active-span", func(t *testing.T) {
			_, ll := testTracer(t)

			// Create a span and get its context
			originalSpan, ctx := ll.StartSpan(context.Background(), llmobs.SpanKindLLM, "test-span", llmobs.StartSpanConfig{})
			defer originalSpan.Finish(llmobs.FinishSpanConfig{})

			// Retrieve the span from context
			retrievedSpan, ok := llmobs.ActiveLLMSpanFromContext(ctx)
			assert.True(t, ok)
			assert.NotNil(t, retrievedSpan)
			assert.Equal(t, originalSpan, retrievedSpan)
		})
		t.Run("start-span-creates-context", func(t *testing.T) {
			_, ll := testTracer(t)

			// StartSpan should automatically add the span to the returned context
			span, ctx := ll.StartSpan(context.Background(), llmobs.SpanKindAgent, "agent-span", llmobs.StartSpanConfig{})
			defer span.Finish(llmobs.FinishSpanConfig{})

			// Verify the span is in the context
			retrievedSpan, ok := llmobs.ActiveLLMSpanFromContext(ctx)
			assert.True(t, ok)
			assert.Equal(t, span, retrievedSpan)
		})

	})
	t.Run("propagated-llm-span-context", func(t *testing.T) {
		t.Run("empty-context", func(t *testing.T) {
			ctx := context.Background()
			propagated, ok := llmobs.PropagatedLLMSpanFromContext(ctx)
			assert.False(t, ok)
			assert.Nil(t, propagated)
		})
		t.Run("with-propagated-span", func(t *testing.T) {
			originalPropagated := &llmobs.PropagatedLLMSpan{
				MLApp:   "test-ml-app",
				TraceID: "trace-123",
				SpanID:  "span-456",
			}

			ctx := llmobs.ContextWithPropagatedLLMSpan(context.Background(), originalPropagated)

			retrievedPropagated, ok := llmobs.PropagatedLLMSpanFromContext(ctx)
			assert.True(t, ok)
			assert.NotNil(t, retrievedPropagated)
			assert.Equal(t, originalPropagated, retrievedPropagated)
			assert.Equal(t, "test-ml-app", retrievedPropagated.MLApp)
			assert.Equal(t, "trace-123", retrievedPropagated.TraceID)
			assert.Equal(t, "span-456", retrievedPropagated.SpanID)
		})

	})
	t.Run("both-active-and-propagated-span-context", func(t *testing.T) {
		_, ll := testTracer(t)

		// Create propagated span first
		propagatedSpan := &llmobs.PropagatedLLMSpan{
			MLApp:   "propagated-app",
			TraceID: "propagated-trace",
			SpanID:  "propagated-span",
		}

		// Add propagated span to context
		ctx := llmobs.ContextWithPropagatedLLMSpan(context.Background(), propagatedSpan)

		// Create active span from context that already has propagated span
		activeSpan, ctx := ll.StartSpan(ctx, llmobs.SpanKindLLM, "active", llmobs.StartSpanConfig{})
		defer activeSpan.Finish(llmobs.FinishSpanConfig{})

		// Both should be retrievable independently
		retrievedActive, activeOk := llmobs.ActiveLLMSpanFromContext(ctx)
		retrievedPropagated, propagatedOk := llmobs.PropagatedLLMSpanFromContext(ctx)

		assert.True(t, activeOk)
		assert.True(t, propagatedOk)
		assert.Equal(t, activeSpan, retrievedActive)
		assert.Equal(t, propagatedSpan, retrievedPropagated)
	})
}

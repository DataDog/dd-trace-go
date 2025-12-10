// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package llmobs_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils/testtracer"
	"github.com/DataDog/dd-trace-go/v2/llmobs"
)

func TestStartSpan(t *testing.T) {
	ctx := context.Background()
	sessionID := "test-session-123"
	mlApp := "test-ml-app"
	modelProvider := "openai"
	modelName := "gpt-4"
	startTime := time.Now().Add(-time.Hour)

	t.Run("llm-span-with-all-options", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		span, spanCtx := llmobs.StartLLMSpan(ctx, "test-llm-span",
			llmobs.WithSessionID(sessionID),
			llmobs.WithMLApp(mlApp),
			llmobs.WithModelProvider(modelProvider),
			llmobs.WithModelName(modelName),
			llmobs.WithStartTime(startTime),
		)
		span.Finish()

		// Verify span properties
		assert.NotEmpty(t, span.SpanID())
		assert.NotEmpty(t, span.TraceID())
		assert.NotEmpty(t, span.APMTraceID())
		assert.Equal(t, "llm", span.Kind())

		// Verify context propagation
		retrievedSpan, ok := llmobs.SpanFromContext(spanCtx)
		assert.True(t, ok)
		assert.Equal(t, span.SpanID(), retrievedSpan.SpanID())

		// Verify type conversion
		llmSpan, ok := retrievedSpan.AsLLM()
		assert.True(t, ok)
		assert.NotNil(t, llmSpan)

		// Should fail to convert to other types
		_, ok = retrievedSpan.AsWorkflow()
		assert.False(t, ok)
		_, ok = retrievedSpan.AsAgent()
		assert.False(t, ok)

		// Verify span was actually sent
		spans := tt.WaitForLLMObsSpans(t, 1)
		require.Len(t, spans, 1)
		assert.Equal(t, "test-llm-span", spans[0].Name)
	})
	t.Run("workflow-span", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		span, spanCtx := llmobs.StartWorkflowSpan(ctx, "test-workflow")
		span.Finish()

		assert.Equal(t, "workflow", span.Kind())

		retrievedSpan, ok := llmobs.SpanFromContext(spanCtx)
		assert.True(t, ok)

		workflowSpan, ok := retrievedSpan.AsWorkflow()
		assert.True(t, ok)
		assert.NotNil(t, workflowSpan)

		// Should fail to convert to LLM
		_, ok = retrievedSpan.AsLLM()
		assert.False(t, ok)

		// Verify span was actually sent
		spans := tt.WaitForLLMObsSpans(t, 1)
		require.Len(t, spans, 1)
		assert.Equal(t, "test-workflow", spans[0].Name)
	})
	t.Run("agent-span", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		span, _ := llmobs.StartAgentSpan(ctx, "test-agent")
		span.Finish()
		assert.Equal(t, "agent", span.Kind())

		// Verify span was actually sent
		spans := tt.WaitForLLMObsSpans(t, 1)
		require.Len(t, spans, 1)
		assert.Equal(t, "test-agent", spans[0].Name)
	})
	t.Run("tool-span", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		span, _ := llmobs.StartToolSpan(ctx, "test-tool")
		span.Finish()
		assert.Equal(t, "tool", span.Kind())

		// Verify span was actually sent
		spans := tt.WaitForLLMObsSpans(t, 1)
		require.Len(t, spans, 1)
		assert.Equal(t, "test-tool", spans[0].Name)
	})
	t.Run("task-span", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		span, _ := llmobs.StartTaskSpan(ctx, "test-task")
		span.Finish()
		assert.Equal(t, "task", span.Kind())

		// Verify span was actually sent
		spans := tt.WaitForLLMObsSpans(t, 1)
		require.Len(t, spans, 1)
		assert.Equal(t, "test-task", spans[0].Name)
	})
	t.Run("embedding-span", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		span, spanCtx := llmobs.StartEmbeddingSpan(ctx, "test-embedding")
		span.Finish()

		assert.Equal(t, "embedding", span.Kind())

		retrievedSpan, ok := llmobs.SpanFromContext(spanCtx)
		assert.True(t, ok)

		embeddingSpan, ok := retrievedSpan.AsEmbedding()
		assert.True(t, ok)
		assert.NotNil(t, embeddingSpan)

		// Verify span was actually sent
		spans := tt.WaitForLLMObsSpans(t, 1)
		require.Len(t, spans, 1)
		assert.Equal(t, "test-embedding", spans[0].Name)
	})
	t.Run("retrieval-span", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		span, spanCtx := llmobs.StartRetrievalSpan(ctx, "test-retrieval")
		span.Finish()

		assert.Equal(t, "retrieval", span.Kind())

		retrievedSpan, ok := llmobs.SpanFromContext(spanCtx)
		assert.True(t, ok)

		retrievalSpan, ok := retrievedSpan.AsRetrieval()
		assert.True(t, ok)
		assert.NotNil(t, retrievalSpan)

		// Verify span was actually sent
		spans := tt.WaitForLLMObsSpans(t, 1)
		require.Len(t, spans, 1)
		assert.Equal(t, "test-retrieval", spans[0].Name)
	})
	t.Run("finish-options", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		span, _ := llmobs.StartLLMSpan(ctx, "finish-options")

		testErr := errors.New("test error")
		finishTime := time.Now().Add(time.Second)

		span.Finish(
			llmobs.WithError(testErr),
			llmobs.WithFinishTime(finishTime),
		)

		// Should not do anything if called more than once
		span.Finish()
		spans := tt.WaitForLLMObsSpans(t, 1)
		require.Len(t, spans, 1)
		assert.Equal(t, "finish-options", spans[0].Name)
		assert.Equal(t, "test error", spans[0].Meta["error.message"])
		assert.NotEmpty(t, spans[0].Meta["error.stack"])
		assert.Equal(t, "*errors.errorString", spans[0].Meta["error.type"])
	})
	t.Run("span-links", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		span, _ := llmobs.StartLLMSpan(ctx, "span-links")

		link := llmobs.SpanLink{
			TraceID: 0x1234567890abcdef,
			SpanID:  0xabcdef1234567890,
		}
		span.AddLink(link)
		span.Finish()

		spans := tt.WaitForLLMObsSpans(t, 1)
		require.Len(t, spans, 1)
		assert.Equal(t, "span-links", spans[0].Name)
		assert.Len(t, spans[0].SpanLinks, 1)
	})
	t.Run("tracer-not-running", func(t *testing.T) {
		// ensure tracer is not running
		tracer.Stop()

		ctx := context.Background()

		// All span creation should return noop spans and not panic
		assert.NotPanics(t, func() {
			span, spanCtx := llmobs.StartLLMSpan(ctx, "noop-llm")
			assert.NotNil(t, span)
			assert.Equal(t, "", span.SpanID()) // noop span returns empty ID
			assert.Equal(t, "", span.Kind())
			span.Finish()

			// Context should not contain LLMObs span
			_, ok := llmobs.SpanFromContext(spanCtx)
			assert.False(t, ok)
		})

		assert.NotPanics(t, func() {
			span, _ := llmobs.StartWorkflowSpan(ctx, "noop-workflow")
			span.AnnotateTextIO("input", "output")
			span.Finish()
		})

		assert.NotPanics(t, func() {
			span, _ := llmobs.StartEmbeddingSpan(ctx, "noop-embedding")
			span.AnnotateEmbeddingIO(nil, "")
			span.Finish()
		})
	})
	t.Run("llmobs-not-enabled", func(t *testing.T) {
		// Start tracer without LLMObs
		tt := testtracer.Start(t, testtracer.WithTracerStartOpts(
			tracer.WithLLMObsEnabled(false),
			tracer.WithService("test-service"),
			tracer.WithLogStartup(false),
		))
		defer tt.Stop()

		// All span creation should return noop spans and not panic
		assert.NotPanics(t, func() {
			span, spanCtx := llmobs.StartLLMSpan(ctx, "noop-llm")
			assert.NotNil(t, span)
			assert.Equal(t, "", span.SpanID()) // noop span returns empty ID
			assert.Equal(t, "", span.Kind())
			span.Finish()

			// Context should not contain LLMObs span
			_, ok := llmobs.SpanFromContext(spanCtx)
			assert.False(t, ok)
		})

		assert.NotPanics(t, func() {
			span, _ := llmobs.StartWorkflowSpan(ctx, "noop-workflow")
			span.AnnotateTextIO("input", "output")
			span.Finish()
		})

		assert.NotPanics(t, func() {
			span, _ := llmobs.StartEmbeddingSpan(ctx, "noop-embedding")
			span.AnnotateEmbeddingIO(nil, "")
			span.Finish()
		})
	})
	t.Run("parent-child-spans", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		// Create parent span
		parentSpan, parentCtx := llmobs.StartWorkflowSpan(ctx, "parent-workflow")
		defer parentSpan.Finish()

		// Create child span from parent context
		childSpan, childCtx := llmobs.StartLLMSpan(parentCtx, "child-llm")
		defer childSpan.Finish()

		// Both spans should be retrievable from their contexts
		retrievedParent, ok := llmobs.SpanFromContext(parentCtx)
		require.True(t, ok)
		assert.Equal(t, parentSpan.SpanID(), retrievedParent.SpanID())

		retrievedChild, ok := llmobs.SpanFromContext(childCtx)
		require.True(t, ok)
		assert.Equal(t, childSpan.SpanID(), retrievedChild.SpanID())

		// Child and parent should have different span IDs but same trace ID
		assert.NotEqual(t, parentSpan.SpanID(), childSpan.SpanID())
		assert.Equal(t, parentSpan.TraceID(), childSpan.TraceID())
	})
}

func TestSpanAnnotations(t *testing.T) {
	ctx := context.Background()

	t.Run("llm-span-annotations", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		span, _ := llmobs.StartLLMSpan(ctx, "test-llm-annotations")

		input := []llmobs.LLMMessage{
			{Role: "user", Content: "Hello"},
		}
		output := []llmobs.LLMMessage{
			{Role: "assistant", Content: "Hi there!"},
		}

		span.AnnotateLLMIO(input, output,
			llmobs.WithAnnotatedTags(map[string]string{"model": "gpt-4"}),
			llmobs.WithAnnotatedMetrics(map[string]float64{
				llmobs.MetricKeyInputTokens:  10,
				llmobs.MetricKeyOutputTokens: 5,
				llmobs.MetricKeyTotalTokens:  15,
			}),
			llmobs.WithAnnotatedMetadata(map[string]any{"temperature": 0.7}),
			llmobs.WithAnnotatedSessionID("session-123"),
		)
		// call it again with empty values to test it does not override info
		span.AnnotateLLMIO(nil, nil)
		span.Finish()

		spans := tt.WaitForLLMObsSpans(t, 1)
		require.Len(t, spans, 1)
		assert.Equal(t, "test-llm-annotations", spans[0].Name)
		assert.Contains(t, spans[0].Meta, "input")
		assert.Contains(t, spans[0].Meta, "output")
		assert.Contains(t, spans[0].Meta["metadata"], "temperature")
		assert.NotEmpty(t, spans[0].Metrics)
		assert.NotEmpty(t, spans[0].Tags)
		assert.Equal(t, "session-123", spans[0].SessionID)
	})
	t.Run("llm-span-with-tool-calls", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		span, _ := llmobs.StartLLMSpan(ctx, "test-llm-with-tools")

		input := []llmobs.LLMMessage{
			{
				Role:    "user",
				Content: "What's the weather in San Francisco?",
			},
			{
				Role:    "assistant",
				Content: "",
				ToolCalls: []llmobs.ToolCall{
					{
						Name:      "get_weather",
						Arguments: []byte(`{"location": "San Francisco", "unit": "celsius"}`),
						ToolID:    "call_123",
						Type:      "function",
					},
				},
			},
		}

		output := []llmobs.LLMMessage{
			{
				Role:    "tool",
				Content: "",
				ToolResults: []llmobs.ToolResult{
					{
						Result: map[string]any{"temperature": 18, "condition": "sunny"},
						Name:   "get_weather",
						ToolID: "call_123",
						Type:   "function",
					},
				},
			},
			{
				Role:    "assistant",
				Content: "The weather in San Francisco is 18°C and sunny.",
			},
		}

		span.AnnotateLLMIO(input, output)
		span.Finish()

		spans := tt.WaitForLLMObsSpans(t, 1)
		require.Len(t, spans, 1)
		assert.Equal(t, "test-llm-with-tools", spans[0].Name)
		assert.Contains(t, spans[0].Meta, "input")
		assert.Contains(t, spans[0].Meta, "output")

		// Verify tool calls are in the payload
		inputMeta, ok := spans[0].Meta["input"].(map[string]any)
		require.True(t, ok, "input should be a map")
		messages, ok := inputMeta["messages"].([]any)
		require.True(t, ok, "input messages should be an array")
		require.Len(t, messages, 2, "should have 2 input messages")

		// Check second message has tool_calls
		assistantMsg, ok := messages[1].(map[string]any)
		require.True(t, ok, "assistant message should be a map")
		assert.Equal(t, "assistant", assistantMsg["role"])
		toolCalls, ok := assistantMsg["tool_calls"].([]any)
		require.True(t, ok, "tool_calls should be present")
		require.Len(t, toolCalls, 1, "should have 1 tool call")

		toolCall, ok := toolCalls[0].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "get_weather", toolCall["name"])
		assert.Equal(t, "call_123", toolCall["tool_id"])
		assert.Equal(t, "function", toolCall["type"])

		// Verify tool results are in the payload
		outputMeta, ok := spans[0].Meta["output"].(map[string]any)
		require.True(t, ok, "output should be a map")
		outputMessages, ok := outputMeta["messages"].([]any)
		require.True(t, ok, "output messages should be an array")
		require.Len(t, outputMessages, 2, "should have 2 output messages")

		// Check first message has tool_results
		toolMsg, ok := outputMessages[0].(map[string]any)
		require.True(t, ok, "tool message should be a map")
		assert.Equal(t, "tool", toolMsg["role"])
		toolResults, ok := toolMsg["tool_results"].([]any)
		require.True(t, ok, "tool_results should be present")
		require.Len(t, toolResults, 1, "should have 1 tool result")

		toolResult, ok := toolResults[0].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "get_weather", toolResult["name"])
		assert.Equal(t, "call_123", toolResult["tool_id"])
		assert.Equal(t, "function", toolResult["type"])
		result, ok := toolResult["result"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, float64(18), result["temperature"])
		assert.Equal(t, "sunny", result["condition"])
	})
	t.Run("llm-span-with-tool-definitions", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		span, _ := llmobs.StartLLMSpan(ctx, "test-llm-with-tool-definitions")

		toolDefs := []llmobs.ToolDefinition{
			{
				Name:        "add_numbers",
				Description: "Add two numbers",
				Schema:      []byte(`{"type": "object", "properties": {"location": {"type": "string"}}}`),
			},
			{
				Name:        "subtract_numbers",
				Description: "Subtract two numbers",
				Schema:      []byte(`{"type": "object", "properties": {"expression": {"type": "string"}}}`),
			},
		}

		input := []llmobs.LLMMessage{
			{Role: "user", Content: "Add 2 and 3"},
		}
		output := []llmobs.LLMMessage{
			{Role: "assistant", Content: "5"},
		}

		span.AnnotateLLMIO(input, output,
			llmobs.WithToolDefinitions(toolDefs),
		)
		span.Finish()

		spans := tt.WaitForLLMObsSpans(t, 1)
		require.Len(t, spans, 1)
		assert.Equal(t, "test-llm-with-tool-definitions", spans[0].Name)

		// Verify tool_definitions are in the metadata
		toolDefinitions, ok := spans[0].Meta["tool_definitions"].([]any)
		require.True(t, ok, "tool_definitions should be present in meta")
		require.Len(t, toolDefinitions, 2, "should have 2 tool definitions")

		// Check first tool definition
		firstTool, ok := toolDefinitions[0].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "add_numbers", firstTool["name"])
		assert.Equal(t, "Add two numbers", firstTool["description"])
		assert.NotNil(t, firstTool["schema"])

		// Check second tool definition
		secondTool, ok := toolDefinitions[1].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "subtract_numbers", secondTool["name"])
		assert.Equal(t, "Subtract two numbers", secondTool["description"])
		assert.NotNil(t, secondTool["schema"])
	})
	t.Run("text-io-span-annotations", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		span, _ := llmobs.StartWorkflowSpan(ctx, "test-workflow-annotations")

		span.AnnotateTextIO("input text", "output text",
			llmobs.WithAnnotatedTags(map[string]string{"version": "1.0"}),
		)
		// call it again with empty values to test it does not override info
		span.AnnotateTextIO("", "")
		span.Finish()

		// Verify span was actually sent
		spans := tt.WaitForLLMObsSpans(t, 1)
		require.Len(t, spans, 1)
		assert.Equal(t, "test-workflow-annotations", spans[0].Name)
		assert.Equal(t, map[string]any{"value": "input text"}, spans[0].Meta["input"])
		assert.Equal(t, map[string]any{"value": "output text"}, spans[0].Meta["output"])
		assert.NotEmpty(t, spans[0].Tags)
	})
	t.Run("embedding-span-annotations", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		span, _ := llmobs.StartEmbeddingSpan(ctx, "test-embedding-annotations")

		docs := []llmobs.EmbeddedDocument{
			{Text: "Document 1", Name: "test-doc.txt", Score: 0.85, ID: "test-1"},
		}
		span.AnnotateEmbeddingIO(docs, "embedding output")

		// call it again with empty values to test it does not override info
		span.AnnotateEmbeddingIO(nil, "")
		span.Finish()

		// Verify span was actually sent
		spans := tt.WaitForLLMObsSpans(t, 1)
		require.Len(t, spans, 1)
		assert.Equal(t, "test-embedding-annotations", spans[0].Name)
		assert.Contains(t, spans[0].Meta, "input")
		assert.Contains(t, spans[0].Meta, "output")
	})
	t.Run("retrieval-span-annotations", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		span, _ := llmobs.StartRetrievalSpan(ctx, "test-retrieval-annotations")

		docs := []llmobs.RetrievedDocument{
			{Text: "Retrieved doc", Name: "result1.txt", Score: 0.95},
		}

		span.AnnotateRetrievalIO("search query", docs)

		// call it again with empty values to test it does not override info
		span.AnnotateRetrievalIO("", nil)
		span.Finish()

		// Verify span was actually sent
		spans := tt.WaitForLLMObsSpans(t, 1)
		require.Len(t, spans, 1)
		assert.Equal(t, "test-retrieval-annotations", spans[0].Name)
		assert.Contains(t, spans[0].Meta, "input")
		assert.Contains(t, spans[0].Meta, "output")
	})
}

func TestEvaluationMetrics(t *testing.T) {
	ctx := context.Background()

	t.Run("evaluation-from-span", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		span, _ := llmobs.StartLLMSpan(ctx, "test-eval-span")
		span.Finish()

		llmobs.SubmitEvaluationFromSpan("accuracy", "correct", span)
		llmobs.SubmitEvaluationFromSpan("score", 0.95, span)
		llmobs.SubmitEvaluationFromSpan("valid", true, span)
		llmobs.SubmitEvaluationFromSpan("count", int32(42), span)
		llmobs.SubmitEvaluationFromSpan("rating", float32(4.5), span)

		// Test with options
		llmobs.SubmitEvaluationFromSpan("quality", "high", span,
			llmobs.WithEvaluationTags([]string{"env:test"}),
			llmobs.WithEvaluationMLApp("eval-app"),
			llmobs.WithEvaluationTimestamp(time.Now()),
		)

		// Verify span was sent
		spans := tt.WaitForLLMObsSpans(t, 1)
		require.Len(t, spans, 1)
		assert.Equal(t, "test-eval-span", spans[0].Name)

		// Verify metrics were sent (6 total: accuracy, score, valid, count, rating, quality)
		metrics := tt.WaitForLLMObsMetrics(t, 6)
		require.Len(t, metrics, 6)

		// Check that we have the expected labels
		labels := make([]string, len(metrics))
		for i, metric := range metrics {
			labels[i] = metric.Label
		}
		assert.Contains(t, labels, "accuracy")
		assert.Contains(t, labels, "score")
		assert.Contains(t, labels, "valid")
		assert.Contains(t, labels, "count")
		assert.Contains(t, labels, "rating")
		assert.Contains(t, labels, "quality")
	})
	t.Run("evaluation-from-tag", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		tag := llmobs.JoinTag{Key: "session_id", Value: "session-123"}

		llmobs.SubmitEvaluationFromTag("feedback", "positive", tag)
		llmobs.SubmitEvaluationFromTag("rating", 4.2, tag)
		llmobs.SubmitEvaluationFromTag("approved", false, tag)

		llmobs.SubmitEvaluationFromTag("sentiment", "neutral", tag,
			llmobs.WithEvaluationTags([]string{"source:user"}),
		)

		metrics := tt.WaitForLLMObsMetrics(t, 4)
		require.Len(t, metrics, 4)

		labels := make([]string, len(metrics))
		for i, metric := range metrics {
			labels[i] = metric.Label
		}
		assert.Contains(t, labels, "feedback")
		assert.Contains(t, labels, "rating")
		assert.Contains(t, labels, "approved")
		assert.Contains(t, labels, "sentiment")
	})
	t.Run("evaluation-with-different-span-types", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		// Test that evaluation works with all span types, not just LLM spans
		llmSpan, _ := llmobs.StartLLMSpan(ctx, "llm-eval")
		workflowSpan, _ := llmobs.StartWorkflowSpan(ctx, "workflow-eval")
		agentSpan, _ := llmobs.StartAgentSpan(ctx, "agent-eval")
		toolSpan, _ := llmobs.StartToolSpan(ctx, "tool-eval")
		taskSpan, _ := llmobs.StartTaskSpan(ctx, "task-eval")
		embeddingSpan, _ := llmobs.StartEmbeddingSpan(ctx, "embedding-eval")
		retrievalSpan, _ := llmobs.StartRetrievalSpan(ctx, "retrieval-eval")

		// Finish all spans
		llmSpan.Finish()
		workflowSpan.Finish()
		agentSpan.Finish()
		toolSpan.Finish()
		taskSpan.Finish()
		embeddingSpan.Finish()
		retrievalSpan.Finish()

		// All span types should work with evaluation metrics
		assert.NotPanics(t, func() {
			llmobs.SubmitEvaluationFromSpan("llm_quality", "good", llmSpan)
			llmobs.SubmitEvaluationFromSpan("workflow_score", 0.9, workflowSpan)
			llmobs.SubmitEvaluationFromSpan("agent_success", true, agentSpan)
			llmobs.SubmitEvaluationFromSpan("tool_rating", 4.5, toolSpan)
			llmobs.SubmitEvaluationFromSpan("task_complete", true, taskSpan)
			llmobs.SubmitEvaluationFromSpan("embedding_accuracy", 0.95, embeddingSpan)
			llmobs.SubmitEvaluationFromSpan("retrieval_relevance", "high", retrievalSpan)
		})

		// Verify all spans were sent (7 total)
		spans := tt.WaitForLLMObsSpans(t, 7)
		require.Len(t, spans, 7)

		// Check that we have all expected span names
		spanNames := make([]string, len(spans))
		for i, span := range spans {
			spanNames[i] = span.Name
		}
		assert.Contains(t, spanNames, "llm-eval")
		assert.Contains(t, spanNames, "workflow-eval")
		assert.Contains(t, spanNames, "agent-eval")
		assert.Contains(t, spanNames, "tool-eval")
		assert.Contains(t, spanNames, "task-eval")
		assert.Contains(t, spanNames, "embedding-eval")
		assert.Contains(t, spanNames, "retrieval-eval")

		// Verify all metrics were sent (7 total)
		metrics := tt.WaitForLLMObsMetrics(t, 7)
		require.Len(t, metrics, 7)

		// Check that we have all expected metric labels
		labels := make([]string, len(metrics))
		for i, metric := range metrics {
			labels[i] = metric.Label
		}
		assert.Contains(t, labels, "llm_quality")
		assert.Contains(t, labels, "workflow_score")
		assert.Contains(t, labels, "agent_success")
		assert.Contains(t, labels, "tool_rating")
		assert.Contains(t, labels, "task_complete")
		assert.Contains(t, labels, "embedding_accuracy")
		assert.Contains(t, labels, "retrieval_relevance")
	})
	t.Run("tracer-not-running", func(t *testing.T) {
		// ensure tracer is not running
		tracer.Stop()

		span, _ := llmobs.StartLLMSpan(ctx, "noop-span")

		// Evaluation submissions should not panic even with noop span
		assert.NotPanics(t, func() {
			llmobs.SubmitEvaluationFromSpan("test", "value", span)
		})

		assert.NotPanics(t, func() {
			tag := llmobs.JoinTag{Key: "test", Value: "value"}
			llmobs.SubmitEvaluationFromTag("test", 1.0, tag)
		})
	})
	t.Run("llmobs-not-enabled", func(t *testing.T) {
		// Start tracer without LLMObs
		tt := testtracer.Start(t, testtracer.WithTracerStartOpts(
			tracer.WithLLMObsEnabled(false),
			tracer.WithService("test-service"),
			tracer.WithLogStartup(false),
		))
		defer tt.Stop()

		span, _ := llmobs.StartLLMSpan(ctx, "noop-span")

		// Evaluation submissions should not panic even with noop span
		assert.NotPanics(t, func() {
			llmobs.SubmitEvaluationFromSpan("test", "value", span)
		})

		assert.NotPanics(t, func() {
			tag := llmobs.JoinTag{Key: "test", Value: "value"}
			llmobs.SubmitEvaluationFromTag("test", 1.0, tag)
		})
	})
}

func testTracer(t *testing.T, opts ...testtracer.Option) *testtracer.TestTracer {
	defaultOpts := []testtracer.Option{
		testtracer.WithTracerStartOpts(
			tracer.WithLLMObsEnabled(true),
			tracer.WithLLMObsMLApp("test-app"),
			tracer.WithLogStartup(false),
		),
		testtracer.WithAgentInfoResponse(testtracer.AgentInfo{
			Endpoints: []string{"/evp_proxy/v2/"},
		}),
	}
	allOpts := append(defaultOpts, opts...)
	tt := testtracer.Start(t, allOpts...)
	t.Cleanup(tt.Stop)
	return tt
}

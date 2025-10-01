// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package llmobs_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils/testtracer"
	"github.com/DataDog/dd-trace-go/v2/internal/llmobs"
	llmobstransport "github.com/DataDog/dd-trace-go/v2/internal/llmobs/transport"
)

const (
	mlApp      = "gotest"
	testAPIKey = "abcd1234efgh5678ijkl9012mnop3456"
)

func TestStartSpan(t *testing.T) {
	t.Run("simple", func(t *testing.T) {
		tt, ll := testTracer(t)
		ctx := context.Background()

		span, _ := ll.StartSpan(ctx, llmobs.SpanKindLLM, "llm-1", llmobs.StartSpanConfig{})
		span.Finish(llmobs.FinishSpanConfig{})

		apmSpans := tt.WaitForSpans(t, 1)
		s0 := apmSpans[0]
		assert.Equal(t, "llm-1", s0.Name)

		llmSpans := tt.WaitForLLMObsSpans(t, 1)
		l0 := llmSpans[0]
		assert.Equal(t, "llm-1", l0.Name)
	})

	t.Run("child-spans", func(t *testing.T) {
		tt, ll := testTracer(t)

		ctx := context.Background()
		ss0, ctx := ll.StartSpan(ctx, llmobs.SpanKindLLM, "llm-1", llmobs.StartSpanConfig{})
		ss1, ctx := ll.StartSpan(ctx, llmobs.SpanKindAgent, "agent-1", llmobs.StartSpanConfig{})
		ss2, ctx := tracer.StartSpanFromContext(ctx, "apm-1")
		ss3, _ := ll.StartSpan(ctx, llmobs.SpanKindLLM, "llm-2", llmobs.StartSpanConfig{})

		ss3.Finish(llmobs.FinishSpanConfig{})
		ss2.Finish()
		ss1.Finish(llmobs.FinishSpanConfig{})
		ss0.Finish(llmobs.FinishSpanConfig{})

		apmSpans := tt.WaitForSpans(t, 4)

		s0 := apmSpans[0]
		s1 := apmSpans[1]
		s2 := apmSpans[2]
		s3 := apmSpans[3]

		assert.Equal(t, "llm-1", s0.Name)
		assert.Equal(t, "agent-1", s1.Name)
		assert.Equal(t, "apm-1", s2.Name)
		assert.Equal(t, "llm-2", s3.Name)

		apmTraceID := s0.TraceID
		assert.Equal(t, apmTraceID, s1.TraceID)
		assert.Equal(t, apmTraceID, s2.TraceID)
		assert.Equal(t, apmTraceID, s3.TraceID)

		llmSpans := tt.WaitForLLMObsSpans(t, 3)

		l0 := llmSpans[0]
		l1 := llmSpans[1]
		l2 := llmSpans[2]

		// FIXME: they are in reverse order
		assert.Equal(t, "llm-2", l0.Name)
		assert.Equal(t, "agent-1", l1.Name)
		assert.Equal(t, "llm-1", l2.Name)

		llmobsTraceID := l0.TraceID
		assert.Equal(t, llmobsTraceID, l1.TraceID)
		assert.Equal(t, llmobsTraceID, l2.TraceID)
	})

	t.Run("distributed-context-propagation", func(t *testing.T) {
		tt, ll := testTracer(t)

		h := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := req.Context()
			ss3, ctx := tracer.StartSpanFromContext(ctx, "apm-2")
			defer ss3.Finish()

			ss4, _ := ll.StartSpan(ctx, llmobs.SpanKindAgent, "agent-1", llmobs.StartSpanConfig{})
			defer ss4.Finish(llmobs.FinishSpanConfig{})

			w.Write([]byte("ok"))
		})
		srv, cl := testClientServer(t, h)

		genSpans := func() {
			ctx := context.Background()
			ss0, ctx := ll.StartSpan(ctx, llmobs.SpanKindLLM, "llm-1", llmobs.StartSpanConfig{MLApp: "custom-ml-app"})
			defer ss0.Finish(llmobs.FinishSpanConfig{})

			ss1, ctx := ll.StartSpan(ctx, llmobs.SpanKindWorkflow, "workflow-1", llmobs.StartSpanConfig{})
			defer ss1.Finish(llmobs.FinishSpanConfig{})

			ss2, ctx := tracer.StartSpanFromContext(ctx, "apm-1")
			defer ss2.Finish()

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/", nil)
			require.NoError(t, err)
			resp, err := cl.Do(req)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			_ = resp.Body.Close()
		}

		genSpans()
		apmSpans := tt.WaitForSpans(t, 7)

		httpServer := apmSpans[0]
		apm2 := apmSpans[1]
		agent1 := apmSpans[2]
		llm1 := apmSpans[3]
		workflow1 := apmSpans[4]
		apm1 := apmSpans[5]
		httpClient := apmSpans[6]

		assert.Equal(t, "http.request", httpServer.Name)
		assert.Equal(t, "server", httpServer.Meta["span.kind"])
		assert.Equal(t, "apm-2", apm2.Name)
		assert.Equal(t, "agent-1", agent1.Name)
		assert.Equal(t, "llm-1", llm1.Name)
		assert.Equal(t, "workflow-1", workflow1.Name)
		assert.Equal(t, "apm-1", apm1.Name)
		assert.Equal(t, "http.request", httpClient.Name)
		assert.Equal(t, "client", httpClient.Meta["span.kind"])

		apmTraceID := httpServer.TraceID
		assert.Equal(t, apmTraceID, apm2.TraceID, "wrong trace ID for span apm-2")
		assert.Equal(t, apmTraceID, agent1.TraceID, "wrong trace ID for span agent-1")
		assert.Equal(t, apmTraceID, llm1.TraceID, "wrong trace ID for span llm-1")
		assert.Equal(t, apmTraceID, workflow1.TraceID, "wrong trace ID for span workflow-1")
		assert.Equal(t, apmTraceID, apm1.TraceID, "wrong trace ID for span apm-1")
		assert.Equal(t, apmTraceID, httpClient.TraceID, "wrong trace ID for span http-client")

		// check correct span linkage
		assert.Equal(t, httpClient.SpanID, httpServer.ParentID)
		assert.Equal(t, httpServer.SpanID, apm2.ParentID)
		assert.Equal(t, apm2.SpanID, agent1.ParentID)

		assert.Equal(t, apm1.SpanID, httpClient.ParentID)
		assert.Equal(t, llm1.SpanID, workflow1.ParentID)
		assert.Equal(t, workflow1.SpanID, apm1.ParentID)
		assert.Equal(t, uint64(0), llm1.ParentID)

		llmSpans := tt.WaitForLLMObsSpans(t, 3)

		l0 := llmSpans[0]
		l1 := llmSpans[1]
		l2 := llmSpans[2]

		assert.Equal(t, "agent-1", l0.Name)
		assert.Equal(t, "custom-ml-app", findTag(l0.Tags, "ml_app"), "wrong ml_app for span agent-1")
		assert.Equal(t, "workflow-1", l1.Name)
		assert.Equal(t, "custom-ml-app", findTag(l1.Tags, "ml_app"), "wrong ml_app for span workflow-1")
		assert.Equal(t, "llm-1", l2.Name)
		assert.Equal(t, "custom-ml-app", findTag(l2.Tags, "ml_app"), "wrong ml_app for span llm-1")

		llmTraceID := l0.TraceID
		assert.Equal(t, llmTraceID, l0.TraceID)
		assert.Equal(t, llmTraceID, l1.TraceID)
	})

	t.Run("custom-start-and-finish-times", func(t *testing.T) {
		tt, ll := testTracer(t)

		ctx := context.Background()

		// Define custom times
		customStartTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
		customFinishTime := customStartTime.Add(5 * time.Second)

		// Start span with custom start time
		span, _ := ll.StartSpan(ctx, llmobs.SpanKindLLM, "", llmobs.StartSpanConfig{
			StartTime: customStartTime,
		})

		// Finish span with custom finish time
		span.Finish(llmobs.FinishSpanConfig{
			FinishTime: customFinishTime,
		})

		// Validate APM span
		apmSpans := tt.WaitForSpans(t, 1)
		s0 := apmSpans[0]
		assert.Equal(t, "llm", s0.Name)
		assert.Equal(t, customStartTime.UnixNano(), s0.Start)
		assert.Equal(t, customFinishTime.Sub(customStartTime).Nanoseconds(), s0.Duration)

		// Validate LLMObs span
		llmSpans := tt.WaitForLLMObsSpans(t, 1)
		l0 := llmSpans[0]
		assert.Equal(t, "llm", l0.Name)
		assert.Equal(t, customStartTime.UnixNano(), l0.StartNS)
		assert.Equal(t, customFinishTime.Sub(customStartTime).Nanoseconds(), l0.Duration)
	})

}

func TestSpanAnnotate(t *testing.T) {
	testCases := []struct {
		name          string
		kind          llmobs.SpanKind
		annotations   llmobs.SpanAnnotations
		config        llmobs.StartSpanConfig
		wantMeta      map[string]any
		wantMetrics   map[string]float64
		wantTags      []string
		wantSessionID string
	}{
		{
			name: "basic-metadata-metrics-tags",
			kind: llmobs.SpanKindAgent,
			annotations: llmobs.SpanAnnotations{
				Metadata: map[string]any{
					"temperature": 0.7,
					"max_tokens":  100,
				},
				Metrics: map[string]float64{
					"input_tokens":  50,
					"output_tokens": 25,
					"total_tokens":  75,
				},
				Tags: map[string]string{
					"model_version": "v1.0",
					"custom_tag":    "custom_value",
				},
			},
			wantMeta: map[string]any{
				"span.kind": "agent",
				"metadata": map[string]any{
					"temperature": 0.7,
					"max_tokens":  float64(100),
				},
			},
			wantMetrics: map[string]float64{
				"input_tokens":  50,
				"output_tokens": 25,
				"total_tokens":  75,
			},
			wantTags: []string{
				"model_version:v1.0",
				"custom_tag:custom_value",
			},
		},
		{
			name: "llm-span-with-text-io",
			kind: llmobs.SpanKindLLM,
			annotations: llmobs.SpanAnnotations{
				InputText:  "input text content",
				OutputText: "output text content",
			},
			wantMeta: map[string]any{
				"span.kind": "llm",
				"input": map[string]any{
					"messages": []any{
						map[string]any{
							"content": "input text content",
							"role":    "",
						},
					},
				},
				"output": map[string]any{
					"messages": []any{
						map[string]any{
							"content": "output text content",
							"role":    "",
						},
					},
				},
			},
		},
		{
			name: "agent-span-with-manifest",
			kind: llmobs.SpanKindAgent,
			annotations: llmobs.SpanAnnotations{
				AgentManifest: "agent-manifest-data",
			},
			wantMeta: map[string]any{
				"span.kind": "agent",
				"metadata": map[string]any{
					"agent_manifest": "agent-manifest-data",
				},
			},
		},
		{
			name: "embedding-span-with-text-output",
			kind: llmobs.SpanKindEmbedding,
			annotations: llmobs.SpanAnnotations{
				OutputText: "embedding-vector-representation",
			},
			wantMeta: map[string]any{
				"span.kind": "embedding",
				"output": map[string]any{
					"value": "embedding-vector-representation",
				},
			},
		},
		{
			name: "retrieval-span-with-text-input",
			kind: llmobs.SpanKindRetrieval,
			annotations: llmobs.SpanAnnotations{
				InputText: "search query",
			},
			wantMeta: map[string]any{
				"span.kind": "retrieval",
				"input": map[string]any{
					"value": "search query",
				},
			},
		},
		{
			name: "experiment-span-with-experiment-data",
			kind: llmobs.SpanKindExperiment,
			annotations: llmobs.SpanAnnotations{
				ExperimentInput: map[string]any{
					"question": "What is AI?",
					"context":  "Technology context",
				},
				ExperimentOutput:         "AI is artificial intelligence",
				ExperimentExpectedOutput: "AI is artificial intelligence technology",
			},
			wantMeta: map[string]any{
				"span.kind": "experiment",
				"input": map[string]any{
					"question": "What is AI?",
					"context":  "Technology context",
				},
				"output":          "AI is artificial intelligence",
				"expected_output": "AI is artificial intelligence technology",
			},
		},
		{
			name: "model-name-and-provider",
			kind: llmobs.SpanKindLLM,
			config: llmobs.StartSpanConfig{
				ModelName:     "gpt-4",
				ModelProvider: "OpenAI",
			},
			wantMeta: map[string]any{
				"span.kind":      "llm",
				"model_name":     "gpt-4",
				"model_provider": "openai",
			},
		},
		{
			name: "prompt-ignored-on-non-llm-span",
			kind: llmobs.SpanKindAgent,
			annotations: llmobs.SpanAnnotations{
				Prompt: &llmobs.Prompt{Template: "test prompt"},
			},
			wantMeta: map[string]any{
				"span.kind": "agent",
			},
		},
		{
			name: "agent-manifest-ignored-on-non-agent-span",
			kind: llmobs.SpanKindLLM,
			annotations: llmobs.SpanAnnotations{
				AgentManifest: "test manifest",
			},
			wantMeta: map[string]any{
				"span.kind": "llm",
			},
		},
		{
			name: "llm-span-with-messages",
			kind: llmobs.SpanKindLLM,
			annotations: llmobs.SpanAnnotations{
				InputMessages: []llmobs.LLMMessage{
					{Role: "user", Content: "What is the capital of France?"},
					{Role: "system", Content: "You are a helpful assistant."},
				},
				OutputMessages: []llmobs.LLMMessage{
					{Role: "assistant", Content: "The capital of France is Paris."},
				},
			},
			wantMeta: map[string]any{
				"span.kind": "llm",
				"input": map[string]any{
					"messages": []any{
						map[string]any{
							"role":    "user",
							"content": "What is the capital of France?",
						},
						map[string]any{
							"role":    "system",
							"content": "You are a helpful assistant.",
						},
					},
				},
				"output": map[string]any{
					"messages": []any{
						map[string]any{
							"role":    "assistant",
							"content": "The capital of France is Paris.",
						},
					},
				},
			},
		},
		{
			name: "embedding-span-with-input-documents",
			kind: llmobs.SpanKindEmbedding,
			annotations: llmobs.SpanAnnotations{
				InputEmbeddedDocs: []llmobs.EmbeddedDocument{
					{Text: "Document 1 content"},
					{Text: "Document 2 content"},
				},
				OutputText: "embedding-vector-representation",
			},
			wantMeta: map[string]any{
				"span.kind": "embedding",
				"input": map[string]any{
					"documents": []any{
						map[string]any{
							"text": "Document 1 content",
						},
						map[string]any{
							"text": "Document 2 content",
						},
					},
				},
				"output": map[string]any{
					"value": "embedding-vector-representation",
				},
			},
		},
		{
			name: "retrieval-span-with-output-documents",
			kind: llmobs.SpanKindRetrieval,
			annotations: llmobs.SpanAnnotations{
				InputText: "search query",
				OutputRetrievedDocs: []llmobs.RetrievedDocument{
					{Text: "Retrieved doc 1", Name: "doc1.txt", Score: 0.95, ID: "doc-1"},
					{Text: "Retrieved doc 2", Name: "doc2.txt", Score: 0.87, ID: "doc-2"},
				},
			},
			wantMeta: map[string]any{
				"span.kind": "retrieval",
				"input": map[string]any{
					"value": "search query",
				},
				"output": map[string]any{
					"documents": []any{
						map[string]any{
							"text":  "Retrieved doc 1",
							"name":  "doc1.txt",
							"score": 0.95,
							"id":    "doc-1",
						},
						map[string]any{
							"text":  "Retrieved doc 2",
							"name":  "doc2.txt",
							"score": 0.87,
							"id":    "doc-2",
						},
					},
				},
			},
		},
		{
			name: "session-id-from-tags",
			kind: llmobs.SpanKindLLM,
			annotations: llmobs.SpanAnnotations{
				Tags: map[string]string{
					llmobs.TagKeySessionID: "custom-session-123",
					"experiment_type":      "qa",
					"custom_tag":           "custom_value",
				},
			},
			wantMeta: map[string]any{
				"span.kind": "llm",
			},
			wantTags: []string{
				"experiment_type:qa",
				"custom_tag:custom_value",
			},
			wantSessionID: "custom-session-123",
		},
		{
			name: "ml-app-from-config",
			kind: llmobs.SpanKindAgent,
			config: llmobs.StartSpanConfig{
				MLApp: "custom-ml-app",
			},
			wantMeta: map[string]any{
				"span.kind": "agent",
			},
			wantTags: []string{
				"ml_app:custom-ml-app",
			},
		},
		{
			name: "model-info-from-config",
			kind: llmobs.SpanKindLLM,
			config: llmobs.StartSpanConfig{
				ModelName:     "gpt-4-turbo",
				ModelProvider: "OpenAI",
			},
			wantMeta: map[string]any{
				"span.kind":      "llm",
				"model_name":     "gpt-4-turbo",
				"model_provider": "openai", // should be lowercased
			},
		},
		{
			name: "embedding-model-info-from-config",
			kind: llmobs.SpanKindEmbedding,
			config: llmobs.StartSpanConfig{
				ModelName:     "text-embedding-ada-002",
				ModelProvider: "OpenAI",
			},
			wantMeta: map[string]any{
				"span.kind":      "embedding",
				"model_name":     "text-embedding-ada-002",
				"model_provider": "openai", // should be lowercased
			},
		},
		{
			name: "session-id-and-ml-app-combined",
			kind: llmobs.SpanKindWorkflow,
			config: llmobs.StartSpanConfig{
				MLApp:     "workflow-app",
				SessionID: "config-session-456",
			},
			annotations: llmobs.SpanAnnotations{
				Tags: map[string]string{
					"workflow_type": "sequential",
				},
			},
			wantMeta: map[string]any{
				"span.kind": "workflow",
			},
			wantTags: []string{
				"ml_app:workflow-app",
				"workflow_type:sequential",
			},
			wantSessionID: "config-session-456",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tt, ll := testTracer(t)
			span, _ := ll.StartSpan(context.Background(), tc.kind, "", tc.config)
			span.Annotate(tc.annotations)
			span.Finish(llmobs.FinishSpanConfig{})

			llmSpans := tt.WaitForLLMObsSpans(t, 1)
			l0 := llmSpans[0]

			if tc.wantMeta != nil {
				for key, expectedValue := range tc.wantMeta {
					assert.Contains(t, l0.Meta, key, "Missing key %q in meta", key)
					assert.Equal(t, expectedValue, l0.Meta[key], "Mismatch for meta key %q", key)
				}
			}

			if tc.wantMetrics != nil {
				for key, expectedValue := range tc.wantMetrics {
					assert.Contains(t, l0.Metrics, key, "Missing key %q in metrics", key)
					assert.Equal(t, expectedValue, l0.Metrics[key], "Mismatch for metrics key %q", key)
				}
			}

			if tc.wantTags != nil {
				for _, expectedTag := range tc.wantTags {
					parts := strings.Split(expectedTag, ":")
					require.Len(t, parts, 2, "Expected tag format 'key:value', got %q", expectedTag)
					expectedKey, expectedValue := parts[0], parts[1]

					actualValue := findTag(l0.Tags, expectedKey)
					assert.Equal(t, expectedValue, actualValue, "Tag %q: expected %q, got %q", expectedKey, expectedValue, actualValue)
				}
			}

			if tc.wantSessionID != "" {
				assert.Equal(t, tc.wantSessionID, l0.SessionID, "Session ID mismatch")
			}
		})
	}
}

func TestSpanTruncation(t *testing.T) {
	t.Run("text-input", func(t *testing.T) {
		tt, ll := testTracer(t)
		ctx := context.Background()
		span, _ := ll.StartSpan(ctx, llmobs.SpanKindTask, "", llmobs.StartSpanConfig{})

		// Create very large strings that will exceed the 5MB size limit when JSON marshaled
		largeContent := strings.Repeat("x", 3_000_000) // 3MB each

		span.Annotate(llmobs.SpanAnnotations{
			InputText:  largeContent,
			OutputText: largeContent,
			Metadata: map[string]any{
				"large_field1": strings.Repeat("a", 1_000_000), // 1MB
				"large_field2": strings.Repeat("b", 1_000_000), // 1MB
			},
		})

		span.Finish(llmobs.FinishSpanConfig{})

		llmSpans := tt.WaitForLLMObsSpans(t, 1)
		l0 := llmSpans[0]

		// Check that input and output were truncated
		if inputMap, ok := l0.Meta["input"].(map[string]any); ok {
			if inputValue, exists := inputMap["value"]; exists {
				assert.Equal(t, "[This value has been dropped because this span's size exceeds the 1MB size limit.]", inputValue)
			}
		}

		if outputMap, ok := l0.Meta["output"].(map[string]any); ok {
			if outputValue, exists := outputMap["value"]; exists {
				assert.Equal(t, "[This value has been dropped because this span's size exceeds the 1MB size limit.]", outputValue)
			}
		}

		// Check that collection errors were set
		assert.Contains(t, l0.CollectionErrors, "dropped_io")

		// Metadata should still be present (only input/output are truncated)
		if metadata, ok := l0.Meta["metadata"].(map[string]any); ok {
			assert.Contains(t, metadata, "large_field1")
			assert.Contains(t, metadata, "large_field2")
		}
	})
	t.Run("llm-messages", func(t *testing.T) {
		tt, ll := testTracer(t)
		ctx := context.Background()
		span, _ := ll.StartSpan(ctx, llmobs.SpanKindLLM, "", llmobs.StartSpanConfig{})

		// Create large messages
		largeContent := strings.Repeat("x", 3_000_000) // 3MB each

		span.Annotate(llmobs.SpanAnnotations{
			InputMessages: []llmobs.LLMMessage{
				{Content: largeContent, Role: "user"},
			},
			OutputMessages: []llmobs.LLMMessage{
				{Content: largeContent, Role: "assistant"},
			},
		})

		span.Finish(llmobs.FinishSpanConfig{})

		llmSpans := tt.WaitForLLMObsSpans(t, 1)
		l0 := llmSpans[0]

		// Should be truncated to {"value": DROPPED_VALUE_TEXT} like Python
		if inputMap, ok := l0.Meta["input"].(map[string]any); ok {
			assert.Equal(t, "[This value has been dropped because this span's size exceeds the 1MB size limit.]", inputMap["value"])
			assert.NotContains(t, inputMap, "messages", "Original messages should be replaced")
		}

		if outputMap, ok := l0.Meta["output"].(map[string]any); ok {
			assert.Equal(t, "[This value has been dropped because this span's size exceeds the 1MB size limit.]", outputMap["value"])
			assert.NotContains(t, outputMap, "messages", "Original messages should be replaced")
		}

		assert.Contains(t, l0.CollectionErrors, "dropped_io")
	})
	t.Run("embedded-docs", func(t *testing.T) {
		tt, ll := testTracer(t)
		ctx := context.Background()
		span, _ := ll.StartSpan(ctx, llmobs.SpanKindEmbedding, "", llmobs.StartSpanConfig{})

		// Create large embedded documents
		largeContent := strings.Repeat("x", 3_000_000) // 3MB each

		span.Annotate(llmobs.SpanAnnotations{
			InputEmbeddedDocs: []llmobs.EmbeddedDocument{
				{Text: largeContent},
				{Text: largeContent},
			},
			OutputText: largeContent,
		})

		span.Finish(llmobs.FinishSpanConfig{})

		llmSpans := tt.WaitForLLMObsSpans(t, 1)
		l0 := llmSpans[0]

		// Should be truncated to {"value": DROPPED_VALUE_TEXT} like Python
		if inputMap, ok := l0.Meta["input"].(map[string]any); ok {
			assert.Equal(t, "[This value has been dropped because this span's size exceeds the 1MB size limit.]", inputMap["value"])
			assert.NotContains(t, inputMap, "documents", "Original documents should be replaced")
		}

		if outputMap, ok := l0.Meta["output"].(map[string]any); ok {
			assert.Equal(t, "[This value has been dropped because this span's size exceeds the 1MB size limit.]", outputMap["value"])
		}

		assert.Contains(t, l0.CollectionErrors, "dropped_io")
	})
	t.Run("retrieved-docs", func(t *testing.T) {
		tt, ll := testTracer(t)
		ctx := context.Background()
		span, _ := ll.StartSpan(ctx, llmobs.SpanKindRetrieval, "", llmobs.StartSpanConfig{})

		// Create large retrieved documents
		largeContent := strings.Repeat("x", 3_000_000) // 3MB each

		span.Annotate(llmobs.SpanAnnotations{
			InputText: "search query",
			OutputRetrievedDocs: []llmobs.RetrievedDocument{
				{Text: largeContent, Name: "doc1.txt", Score: 0.95, ID: "doc-1"},
				{Text: largeContent, Name: "doc2.txt", Score: 0.87, ID: "doc-2"},
			},
		})

		span.Finish(llmobs.FinishSpanConfig{})

		llmSpans := tt.WaitForLLMObsSpans(t, 1)
		l0 := llmSpans[0]

		// Should be truncated to {"value": DROPPED_VALUE_TEXT} like Python
		if inputMap, ok := l0.Meta["input"].(map[string]any); ok {
			assert.Equal(t, "[This value has been dropped because this span's size exceeds the 1MB size limit.]", inputMap["value"])
		}

		if outputMap, ok := l0.Meta["output"].(map[string]any); ok {
			assert.Equal(t, "[This value has been dropped because this span's size exceeds the 1MB size limit.]", outputMap["value"])
			assert.NotContains(t, outputMap, "documents", "Original documents should be replaced")
		}

		assert.Contains(t, l0.CollectionErrors, "dropped_io")
	})
}

func TestPropagatedInfo(t *testing.T) {
	t.Run("trace-id-from-parent", func(t *testing.T) {
		_, ll := testTracer(t)
		ctx := context.Background()

		// Create parent span
		parentSpan, ctx := ll.StartSpan(ctx, llmobs.SpanKindWorkflow, "parent", llmobs.StartSpanConfig{})
		parentTraceID := parentSpan.TraceID()

		// Create child span - should inherit trace ID
		childSpan, _ := ll.StartSpan(ctx, llmobs.SpanKindLLM, "child", llmobs.StartSpanConfig{})

		assert.Equal(t, parentTraceID, childSpan.TraceID(), "Child should inherit parent's trace ID")

		parentSpan.Finish(llmobs.FinishSpanConfig{})
		childSpan.Finish(llmobs.FinishSpanConfig{})
	})

	t.Run("trace-id-from-propagated", func(t *testing.T) {
		_, ll := testTracer(t)
		ctx := context.Background()

		// Create propagated span context
		propagated := &llmobs.PropagatedLLMSpan{
			TraceID: "propagated-trace-123",
			SpanID:  "propagated-span-456",
			MLApp:   "propagated-app",
		}
		ctx = llmobs.ContextWithPropagatedLLMSpan(ctx, propagated)

		// Create span - should inherit propagated trace ID
		span, _ := ll.StartSpan(ctx, llmobs.SpanKindLLM, "span", llmobs.StartSpanConfig{})

		assert.Equal(t, "propagated-trace-123", span.TraceID(), "Should inherit propagated trace ID")

		span.Finish(llmobs.FinishSpanConfig{})
	})

	t.Run("ml-app-precedence", func(t *testing.T) {
		// Test precedence: config > parent > propagated > global
		t.Run("config-overrides-all", func(t *testing.T) {
			_, ll := testTracer(t)
			ctx := context.Background()

			// Create parent with ML App
			parentSpan, ctx := ll.StartSpan(ctx, llmobs.SpanKindWorkflow, "parent", llmobs.StartSpanConfig{
				MLApp: "parent-app",
			})

			// Add propagated span with different ML App
			propagated := &llmobs.PropagatedLLMSpan{
				MLApp:   "propagated-app",
				TraceID: "trace-123",
				SpanID:  "span-456",
			}
			ctx = llmobs.ContextWithPropagatedLLMSpan(ctx, propagated)

			// Create child with explicit ML App - should use config value
			childSpan, _ := ll.StartSpan(ctx, llmobs.SpanKindLLM, "child", llmobs.StartSpanConfig{
				MLApp: "config-app",
			})

			assert.Equal(t, "config-app", childSpan.MLApp(), "Config ML App should take precedence")

			parentSpan.Finish(llmobs.FinishSpanConfig{})
			childSpan.Finish(llmobs.FinishSpanConfig{})
		})

		t.Run("parent-overrides-propagated", func(t *testing.T) {
			_, ll := testTracer(t)
			ctx := context.Background()

			// Create parent with ML App
			parentSpan, ctx := ll.StartSpan(ctx, llmobs.SpanKindWorkflow, "parent", llmobs.StartSpanConfig{
				MLApp: "parent-app",
			})

			// Add propagated span with different ML App
			propagated := &llmobs.PropagatedLLMSpan{
				MLApp:   "propagated-app",
				TraceID: "trace-123",
				SpanID:  "span-456",
			}
			ctx = llmobs.ContextWithPropagatedLLMSpan(ctx, propagated)

			// Create child without explicit ML App - should use parent's
			childSpan, _ := ll.StartSpan(ctx, llmobs.SpanKindLLM, "child", llmobs.StartSpanConfig{})

			assert.Equal(t, "parent-app", childSpan.MLApp(), "Parent ML App should override propagated")

			parentSpan.Finish(llmobs.FinishSpanConfig{})
			childSpan.Finish(llmobs.FinishSpanConfig{})
		})

		t.Run("propagated-overrides-global", func(t *testing.T) {
			_, ll := testTracer(t)
			ctx := context.Background()

			// Add propagated span with ML App
			propagated := &llmobs.PropagatedLLMSpan{
				MLApp:   "propagated-app",
				TraceID: "trace-123",
				SpanID:  "span-456",
			}
			ctx = llmobs.ContextWithPropagatedLLMSpan(ctx, propagated)

			// Create span without explicit ML App - should use propagated
			span, _ := ll.StartSpan(ctx, llmobs.SpanKindLLM, "span", llmobs.StartSpanConfig{})

			assert.Equal(t, "propagated-app", span.MLApp(), "Propagated ML App should override global")

			span.Finish(llmobs.FinishSpanConfig{})
		})
	})

	t.Run("session-id-precedence", func(t *testing.T) {
		t.Run("config-overrides-parent", func(t *testing.T) {
			tt, ll := testTracer(t)
			ctx := context.Background()

			// Create parent with session ID
			parentSpan, ctx := ll.StartSpan(ctx, llmobs.SpanKindWorkflow, "parent", llmobs.StartSpanConfig{
				SessionID: "parent-session",
			})

			// Create child with explicit session ID - should use config value
			childSpan, _ := ll.StartSpan(ctx, llmobs.SpanKindLLM, "child", llmobs.StartSpanConfig{
				SessionID: "config-session",
			})

			childSpan.Finish(llmobs.FinishSpanConfig{})
			parentSpan.Finish(llmobs.FinishSpanConfig{})

			llmSpans := tt.WaitForLLMObsSpans(t, 2)

			// Find the child span (should be first due to finish order)
			var childLLMSpan *testtracer.LLMObsSpan
			for i := range llmSpans {
				if llmSpans[i].Name == "child" {
					childLLMSpan = &llmSpans[i]
					break
				}
			}
			require.NotNil(t, childLLMSpan, "Child span should be found")
			assert.Equal(t, "config-session", childLLMSpan.SessionID, "Config session ID should take precedence")
		})
		t.Run("parent-session-id-inherited", func(t *testing.T) {
			tt, ll := testTracer(t)
			ctx := context.Background()

			// Create parent with session ID
			parentSpan, ctx := ll.StartSpan(ctx, llmobs.SpanKindWorkflow, "parent", llmobs.StartSpanConfig{
				SessionID: "parent-session",
			})

			// Create child without explicit session ID - should inherit from parent
			childSpan, _ := ll.StartSpan(ctx, llmobs.SpanKindLLM, "child", llmobs.StartSpanConfig{})

			childSpan.Finish(llmobs.FinishSpanConfig{})
			parentSpan.Finish(llmobs.FinishSpanConfig{})

			llmSpans := tt.WaitForLLMObsSpans(t, 2)

			// Find the child span
			var childLLMSpan *testtracer.LLMObsSpan
			for i := range llmSpans {
				if llmSpans[i].Name == "child" {
					childLLMSpan = &llmSpans[i]
					break
				}
			}
			require.NotNil(t, childLLMSpan, "Child span should be found")
			assert.Equal(t, "parent-session", childLLMSpan.SessionID, "Should inherit parent's session ID")
		})

		t.Run("session-id-from-tags", func(t *testing.T) {
			tt, ll := testTracer(t)
			ctx := context.Background()

			// Create span and annotate with session ID via tags
			span, _ := ll.StartSpan(ctx, llmobs.SpanKindLLM, "span", llmobs.StartSpanConfig{})

			span.Annotate(llmobs.SpanAnnotations{
				Tags: map[string]string{
					llmobs.TagKeySessionID: "tags-session",
				},
			})

			span.Finish(llmobs.FinishSpanConfig{})

			llmSpans := tt.WaitForLLMObsSpans(t, 1)
			assert.Equal(t, "tags-session", llmSpans[0].SessionID, "Session ID should be set from tags")
		})
	})
	t.Run("multi-level-propagation", func(t *testing.T) {
		tt, ll := testTracer(t)

		ctx := context.Background()

		// Create grandparent span
		grandparentSpan, ctx := ll.StartSpan(ctx, llmobs.SpanKindWorkflow, "grandparent", llmobs.StartSpanConfig{
			MLApp:     "grandparent-app",
			SessionID: "grandparent-session",
		})

		// Create parent span (no explicit values - should inherit)
		parentSpan, ctx := ll.StartSpan(ctx, llmobs.SpanKindAgent, "parent", llmobs.StartSpanConfig{})

		// Create child span (no explicit values - should inherit from grandparent through parent)
		childSpan, _ := ll.StartSpan(ctx, llmobs.SpanKindLLM, "child", llmobs.StartSpanConfig{})

		assert.Equal(t, "grandparent-app", childSpan.MLApp(), "Should inherit ML App through parent chain")

		childSpan.Finish(llmobs.FinishSpanConfig{})
		parentSpan.Finish(llmobs.FinishSpanConfig{})
		grandparentSpan.Finish(llmobs.FinishSpanConfig{})

		llmSpans := tt.WaitForLLMObsSpans(t, 3)

		// Find child span and verify session ID propagation
		var childLLMSpan *testtracer.LLMObsSpan
		for i := range llmSpans {
			if llmSpans[i].Name == "child" {
				childLLMSpan = &llmSpans[i]
				break
			}
		}
		require.NotNil(t, childLLMSpan, "Child span should be found")
		assert.Equal(t, "grandparent-session", childLLMSpan.SessionID, "Should inherit session ID through parent chain")
	})
	t.Run("mixed-propagation-sources", func(t *testing.T) {
		tt, ll := testTracer(t)
		ctx := context.Background()

		// Add propagated span context
		propagated := &llmobs.PropagatedLLMSpan{
			TraceID: "propagated-trace",
			SpanID:  "propagated-span",
			MLApp:   "propagated-app",
		}
		ctx = llmobs.ContextWithPropagatedLLMSpan(ctx, propagated)

		// Create parent span with session ID but no ML App
		parentSpan, ctx := ll.StartSpan(ctx, llmobs.SpanKindWorkflow, "parent", llmobs.StartSpanConfig{
			SessionID: "parent-session",
		})

		// Create child span - should get ML App from propagated and session ID from parent
		childSpan, _ := ll.StartSpan(ctx, llmobs.SpanKindLLM, "child", llmobs.StartSpanConfig{})

		assert.Equal(t, "propagated-trace", childSpan.TraceID(), "Should use propagated trace ID")
		assert.Equal(t, "propagated-app", childSpan.MLApp(), "Should use propagated ML App")

		childSpan.Finish(llmobs.FinishSpanConfig{})
		parentSpan.Finish(llmobs.FinishSpanConfig{})

		llmSpans := tt.WaitForLLMObsSpans(t, 2)

		// Find child span and verify session ID from parent
		var childLLMSpan *testtracer.LLMObsSpan
		for i := range llmSpans {
			if llmSpans[i].Name == "child" {
				childLLMSpan = &llmSpans[i]
				break
			}
		}
		require.NotNil(t, childLLMSpan, "Child span should be found")
		assert.Equal(t, "parent-session", childLLMSpan.SessionID, "Should inherit session ID from parent")
	})
}

func TestSubmitEvaluation(t *testing.T) {
	testCases := []struct {
		name       string
		config     llmobs.EvaluationConfig
		wantError  string
		wantMetric func() llmobstransport.LLMObsMetric
	}{
		{
			name: "span-join-categorical",
			config: llmobs.EvaluationConfig{
				SpanID:           "test-span-id",
				TraceID:          "test-trace-id",
				Label:            "accuracy",
				CategoricalValue: ptrFromVal("correct"),
				MLApp:            "test-app",
				TimestampMS:      1234567890,
				Tags:             []string{"env:test"},
			},
			wantMetric: func() llmobstransport.LLMObsMetric {
				return llmobstransport.LLMObsMetric{
					JoinOn: llmobstransport.EvaluationJoinOn{
						Span: &llmobstransport.EvaluationSpanJoin{
							SpanID:  "test-span-id",
							TraceID: "test-trace-id",
						},
					},
					MetricType:       "categorical",
					Label:            "accuracy",
					CategoricalValue: ptrFromVal("correct"),
					MLApp:            "test-app",
					TimestampMS:      1234567890,
					Tags:             []string{"env:test"},
				}
			},
		},
		{
			name: "span-join-score",
			config: llmobs.EvaluationConfig{
				SpanID:      "test-span-id",
				TraceID:     "test-trace-id",
				Label:       "rating",
				ScoreValue:  ptrFromVal(0.85),
				MLApp:       "test-app",
				TimestampMS: 1234567890,
			},
			wantMetric: func() llmobstransport.LLMObsMetric {
				return llmobstransport.LLMObsMetric{
					JoinOn: llmobstransport.EvaluationJoinOn{
						Span: &llmobstransport.EvaluationSpanJoin{
							SpanID:  "test-span-id",
							TraceID: "test-trace-id",
						},
					},
					MetricType:  "score",
					Label:       "rating",
					ScoreValue:  ptrFromVal(0.85),
					MLApp:       "test-app",
					TimestampMS: 1234567890,
				}
			},
		},
		{
			name: "span-join-boolean",
			config: llmobs.EvaluationConfig{
				SpanID:       "test-span-id",
				TraceID:      "test-trace-id",
				Label:        "is_valid",
				BooleanValue: ptrFromVal(true),
				MLApp:        "test-app",
				TimestampMS:  1234567890,
			},
			wantMetric: func() llmobstransport.LLMObsMetric {
				return llmobstransport.LLMObsMetric{
					JoinOn: llmobstransport.EvaluationJoinOn{
						Span: &llmobstransport.EvaluationSpanJoin{
							SpanID:  "test-span-id",
							TraceID: "test-trace-id",
						},
					},
					MetricType:   "boolean",
					Label:        "is_valid",
					BooleanValue: ptrFromVal(true),
					MLApp:        "test-app",
					TimestampMS:  1234567890,
				}
			},
		},
		{
			name: "tag-join-categorical",
			config: llmobs.EvaluationConfig{
				TagKey:           "session_id",
				TagValue:         "session-123",
				Label:            "quality",
				CategoricalValue: ptrFromVal("high"),
				MLApp:            "test-app",
				TimestampMS:      1234567890,
			},
			wantMetric: func() llmobstransport.LLMObsMetric {
				return llmobstransport.LLMObsMetric{
					JoinOn: llmobstransport.EvaluationJoinOn{
						Tag: &llmobstransport.EvaluationTagJoin{
							Key:   "session_id",
							Value: "session-123",
						},
					},
					MetricType:       "categorical",
					Label:            "quality",
					CategoricalValue: ptrFromVal("high"),
					MLApp:            "test-app",
					TimestampMS:      1234567890,
				}
			},
		},
		{
			name: "missing-join-info",
			config: llmobs.EvaluationConfig{
				Label:            "test",
				CategoricalValue: ptrFromVal("value"),
			},
			wantError: "must provide either span/trace IDs or tag key/value for joining",
		},
		{
			name: "both-join-methods",
			config: llmobs.EvaluationConfig{
				SpanID:           "test-span-id",
				TraceID:          "test-trace-id",
				TagKey:           "session_id",
				TagValue:         "session-123",
				Label:            "test",
				CategoricalValue: ptrFromVal("value"),
			},
			wantError: "provide either span/trace IDs or tag key/value, not both",
		},
		{
			name: "no-value-provided",
			config: llmobs.EvaluationConfig{
				SpanID:  "test-span-id",
				TraceID: "test-trace-id",
				Label:   "test",
			},
			wantError: "exactly one metric value (categorical, score, or boolean) must be provided",
		},
		{
			name: "multiple-values-provided",
			config: llmobs.EvaluationConfig{
				SpanID:           "test-span-id",
				TraceID:          "test-trace-id",
				Label:            "test",
				CategoricalValue: ptrFromVal("value"),
				ScoreValue:       ptrFromVal(0.5),
			},
			wantError: "exactly one metric value (categorical, score, or boolean) must be provided",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tt, ll := testTracer(t)

			err := ll.SubmitEvaluation(tc.config)
			if tc.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantError)
				return
			}
			require.NoError(t, err)

			got := tt.WaitForLLMObsMetrics(t, 1)
			require.Len(t, got, 1)

			assert.Equal(t, tc.wantMetric(), got[0])
		})
	}
}

func TestLLMObsLifecycle(t *testing.T) {
	t.Run("start-stop", func(t *testing.T) {
		// Ensure no active LLMObs initially
		_, err := llmobs.ActiveLLMObs()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "LLMObs is not enabled")

		// Start LLMObs
		tt := testtracer.Start(t,
			testtracer.WithTracerStartOpts(
				tracer.WithLLMObsEnabled(true),
				tracer.WithLLMObsMLApp("test-app"),
				tracer.WithLogStartup(false),
				tracer.WithLLMObsAgentlessEnabled(false),
			),
		)
		defer tt.Stop()

		// Now should have active LLMObs
		ll, err := llmobs.ActiveLLMObs()
		require.NoError(t, err)
		assert.NotNil(t, ll)
		assert.Equal(t, "test-app", ll.Config.MLApp)

		// Stop LLMObs
		llmobs.Stop()

		// Should no longer have active LLMObs
		_, err = llmobs.ActiveLLMObs()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "LLMObs is not enabled")
	})
	t.Run("multiple-start-stop", func(t *testing.T) {
		// Start first instance
		tt1 := testtracer.Start(t,
			testtracer.WithTracerStartOpts(
				tracer.WithLLMObsEnabled(true),
				tracer.WithLLMObsMLApp("app1"),
				tracer.WithLogStartup(false),
				tracer.WithLLMObsAgentlessEnabled(false),
			),
		)
		defer tt1.Stop()

		ll1, err := llmobs.ActiveLLMObs()
		require.NoError(t, err)
		assert.Equal(t, "app1", ll1.Config.MLApp)

		// Start second instance (should replace first)
		tt2 := testtracer.Start(t,
			testtracer.WithTracerStartOpts(
				tracer.WithLLMObsEnabled(true),
				tracer.WithLLMObsMLApp("app2"),
				tracer.WithLogStartup(false),
			),
		)
		defer tt2.Stop()

		ll2, err := llmobs.ActiveLLMObs()
		require.NoError(t, err)
		assert.Equal(t, "app2", ll2.Config.MLApp)
		assert.NotEqual(t, ll1, ll2) // Should be different instances

		// Stop and verify
		llmobs.Stop()
		_, err = llmobs.ActiveLLMObs()
		assert.Error(t, err)
	})
	t.Run("flush", func(t *testing.T) {
		tt := testtracer.Start(t,
			testtracer.WithTracerStartOpts(
				tracer.WithLLMObsEnabled(true),
				tracer.WithLLMObsMLApp("flush-test"),
				tracer.WithLogStartup(false),
				tracer.WithLLMObsAgentlessEnabled(false),
			),
		)
		defer tt.Stop()

		ll, err := llmobs.ActiveLLMObs()
		require.NoError(t, err)

		// Create a span but don't wait for automatic flush
		ctx := context.Background()
		span, _ := ll.StartSpan(ctx, llmobs.SpanKindTask, "flush-test-span", llmobs.StartSpanConfig{})
		span.Finish(llmobs.FinishSpanConfig{})

		// Use tracer.Flush instead of llmobs.Flush to test the integration:
		// This ensures that the main tracer's Flush() properly calls llmobs.Flush()
		// when LLMObs is enabled, which is the expected behavior in real usage.
		tracer.Flush()

		// Verify span was flushed immediately
		assert.Eventually(t, func() bool {
			return len(tt.Payloads.LLMSpans) == 1
		}, 100*time.Millisecond, 10*time.Millisecond, "Expected LLMObs span to be flushed immediately")
		assert.Equal(t, "flush-test-span", tt.Payloads.LLMSpans[0].Name)
	})
	t.Run("flush-without-active-llmobs", func(t *testing.T) {
		// Ensure no active LLMObs
		llmobs.Stop()

		// Should not panic when calling Flush with no active LLMObs
		assert.NotPanics(t, func() {
			llmobs.Flush()
		})
	})
	t.Run("stop-without-active-llmobs", func(t *testing.T) {
		// Ensure no active LLMObs
		llmobs.Stop()

		// Should not panic when calling Stop with no active LLMObs
		assert.NotPanics(t, func() {
			llmobs.Stop()
		})
	})
	t.Run("tracer-stop-integration", func(t *testing.T) {
		_ = testtracer.Start(t,
			testtracer.WithTracerStartOpts(
				tracer.WithLLMObsEnabled(true),
				tracer.WithLLMObsMLApp("stop-test"),
				tracer.WithLogStartup(false),
				tracer.WithLLMObsAgentlessEnabled(false),
			),
		)

		// Verify LLMObs is active
		ll, err := llmobs.ActiveLLMObs()
		require.NoError(t, err)
		assert.Equal(t, "stop-test", ll.Config.MLApp)

		// Use tracer.Stop instead of tt.Stop to test the integration:
		// This ensures that the main tracer's Stop() properly calls llmobs.Stop()
		// when LLMObs is enabled, which is the expected behavior in real usage.
		tracer.Stop()

		// Verify LLMObs was stopped
		_, err = llmobs.ActiveLLMObs()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "LLMObs is not enabled")
	})
	t.Run("llmobs-disabled", func(t *testing.T) {
		// Start tracer without LLMObs enabled
		tt := testtracer.Start(t,
			testtracer.WithTracerStartOpts(
				tracer.WithLLMObsEnabled(false),
				tracer.WithLogStartup(false),
			),
		)
		defer tt.Stop()

		// Should not have active LLMObs
		_, err := llmobs.ActiveLLMObs()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "LLMObs is not enabled")

		// Flush should not panic when LLMObs is disabled
		assert.NotPanics(t, func() {
			tracer.Flush()
		})

		// Stop should not panic when LLMObs is disabled
		assert.NotPanics(t, func() {
			llmobs.Stop()
		})
	})
	t.Run("llmobs-enabled-without-ml-app", func(t *testing.T) {
		// Start tracer directly with LLMObs enabled but no ML app - should return error
		err := tracer.Start(
			tracer.WithLLMObsEnabled(true),
			tracer.WithLogStartup(false),
			tracer.WithLLMObsAgentlessEnabled(false),
		)
		defer tracer.Stop()

		// Should get error from tracer.Start due to missing ML app
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ML App is required")

		// Should not have active LLMObs due to startup failure
		_, err = llmobs.ActiveLLMObs()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "LLMObs is not enabled")
	})
	t.Run("env-vars-config", func(t *testing.T) {
		t.Setenv("DD_LLMOBS_ENABLED", "true")
		t.Setenv("DD_LLMOBS_ML_APP", "env-test-app")
		t.Setenv("DD_LLMOBS_AGENTLESS_ENABLED", "false")

		tt := testtracer.Start(t,
			testtracer.WithTracerStartOpts(
				tracer.WithLogStartup(false),
			),
		)
		defer tt.Stop()

		ll, err := llmobs.ActiveLLMObs()
		require.NoError(t, err)
		assert.Equal(t, "env-test-app", ll.Config.MLApp)
		assert.True(t, ll.Config.Enabled)
		require.NotNil(t, ll.Config.AgentlessEnabled, "AgentlessEnabled should not be nil when set via env var")
		assert.False(t, *ll.Config.AgentlessEnabled, "Should respect DD_LLMOBS_AGENTLESS_ENABLED=false")
		assert.False(t, ll.Config.ResolvedAgentlessEnabled, "Should resolve to agentless=false")

		ctx := context.Background()
		span, _ := ll.StartSpan(ctx, llmobs.SpanKindTask, "env-test-span", llmobs.StartSpanConfig{})
		span.Finish(llmobs.FinishSpanConfig{})

		llmSpans := tt.WaitForLLMObsSpans(t, 1)
		require.Len(t, llmSpans, 1)
		assert.Equal(t, "env-test-span", llmSpans[0].Name)
	})
	t.Run("env-vars-disabled", func(t *testing.T) {
		t.Setenv("DD_LLMOBS_ENABLED", "false")
		t.Setenv("DD_LLMOBS_ML_APP", "should-be-ignored")

		tt := testtracer.Start(t,
			testtracer.WithTracerStartOpts(
				tracer.WithLogStartup(false),
			),
		)
		defer tt.Stop()

		_, err := llmobs.ActiveLLMObs()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "LLMObs is not enabled")
	})
	t.Run("code-config-overrides-env-vars", func(t *testing.T) {
		t.Setenv("DD_LLMOBS_ENABLED", "false")
		t.Setenv("DD_LLMOBS_ML_APP", "env-app")

		tt := testtracer.Start(t,
			testtracer.WithTracerStartOpts(
				tracer.WithLLMObsEnabled(true),
				tracer.WithLLMObsMLApp("code-app"),
				tracer.WithLLMObsAgentlessEnabled(false),
				tracer.WithLogStartup(false),
			),
		)
		defer tt.Stop()

		ll, err := llmobs.ActiveLLMObs()
		require.NoError(t, err)
		assert.Equal(t, "code-app", ll.Config.MLApp)
		assert.True(t, ll.Config.Enabled)
	})
	t.Run("agentless-defaults-false-when-evp-proxy-available", func(t *testing.T) {
		// When agent supports evp_proxy/v2, should default to agentless=false
		tt := testtracer.Start(t,
			testtracer.WithTracerStartOpts(
				tracer.WithLLMObsEnabled(true),
				tracer.WithLLMObsMLApp("agentless-test"),
				tracer.WithLogStartup(false),
			),
			testtracer.WithAgentInfoResponse(testtracer.AgentInfo{
				Endpoints: []string{"/evp_proxy/v2/"}, // Agent supports evp_proxy
			}),
		)
		defer tt.Stop()

		ll, err := llmobs.ActiveLLMObs()
		require.NoError(t, err)
		assert.Nil(t, ll.Config.AgentlessEnabled, "AgentlessEnabled should be nil when not explicitly set")
		assert.False(t, ll.Config.ResolvedAgentlessEnabled, "Should default to agentless=false when agent supports evp_proxy")
	})
	t.Run("agentless-defaults-true-when-evp-proxy-unavailable", func(t *testing.T) {
		// Set valid API key (32 chars, lowercase + numbers only)
		t.Setenv("DD_API_KEY", testAPIKey)

		// When agent doesn't support evp_proxy/v2, should default to agentless=true
		err := tracer.Start(
			tracer.WithLLMObsEnabled(true),
			tracer.WithLLMObsMLApp("agentless-test"),
			tracer.WithLogStartup(false),
		)
		defer tracer.Stop()

		require.NoError(t, err)

		ll, err := llmobs.ActiveLLMObs()
		require.NoError(t, err)
		assert.Nil(t, ll.Config.AgentlessEnabled, "AgentlessEnabled should be nil when not explicitly set")
		assert.True(t, ll.Config.ResolvedAgentlessEnabled, "Should default to agentless=true when agent doesn't support evp_proxy")
	})
	t.Run("agentless-fails-with-invalid-api-key", func(t *testing.T) {
		// Set invalid API key (wrong length)
		t.Setenv("DD_API_KEY", "invalid-key")

		// When defaulting to agentless with invalid API key, should fail
		err := tracer.Start(
			tracer.WithLLMObsEnabled(true),
			tracer.WithLLMObsMLApp("agentless-test"),
			tracer.WithLogStartup(false),
		)
		defer tracer.Stop()

		// Should get error due to invalid API key in agentless mode
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "agentless mode requires a valid API key")

		// Should not have active LLMObs due to startup failure
		_, err = llmobs.ActiveLLMObs()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "LLMObs is not enabled")
	})
	t.Run("agentless-fails-without-api-key", func(t *testing.T) {
		// enforce an empty DD_API_KEY
		t.Setenv("DD_API_KEY", "")

		// When defaulting to agentless but no API key is provided, should fail
		err := tracer.Start(
			tracer.WithLLMObsEnabled(true),
			tracer.WithLLMObsMLApp("agentless-test"),
			tracer.WithLogStartup(false),
			// Intentionally not setting API key
		)
		defer tracer.Stop()

		// Should get error due to missing API key in agentless mode
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "agentless mode requires a valid API key")

		// Should not have active LLMObs due to startup failure
		_, err = llmobs.ActiveLLMObs()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "LLMObs is not enabled")
	})
	t.Run("explicit-agentless-overrides-default", func(t *testing.T) {
		tt := testtracer.Start(t,
			testtracer.WithTracerStartOpts(
				tracer.WithLLMObsEnabled(true),
				tracer.WithLLMObsMLApp("agentless-test"),
				tracer.WithLLMObsAgentlessEnabled(false),
				tracer.WithLogStartup(false),
			),
		)
		defer tt.Stop()

		ll, err := llmobs.ActiveLLMObs()
		require.NoError(t, err)
		require.NotNil(t, ll.Config.AgentlessEnabled, "AgentlessEnabled should not be nil when explicitly set")
		assert.False(t, *ll.Config.AgentlessEnabled, "Explicit agentless=false should override default")
		assert.False(t, ll.Config.ResolvedAgentlessEnabled, "Explicit agentless=false should override default")
	})
}

func BenchmarkStartSpan(b *testing.B) {
	run := func(b *testing.B, ll *llmobs.LLMObs, tt *testtracer.TestTracer, done chan struct{}) {
		b.Log("starting benchmark")

		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			span, _ := ll.StartSpan(context.Background(), llmobs.SpanKindLLM, fmt.Sprintf("span-%d", i), llmobs.StartSpanConfig{})
			span.Finish(llmobs.FinishSpanConfig{})
		}
		b.StopTimer()

		b.Log("finished benchmark")

		b.Log("waiting for spans")

		tt.WaitFor(b, 10*time.Second, func(payloads *testtracer.Payloads) bool {
			return len(payloads.Spans) > 0 && len(payloads.LLMSpans) > 0
		})
	}

	b.Run("basic", func(b *testing.B) {
		tt, ll := testTracer(b, testtracer.WithRequestDelay(500*time.Millisecond))
		done := make(chan struct{})
		run(b, ll, tt, done)
	})
	b.Run("periodic-flush", func(b *testing.B) {
		tt, ll := testTracer(b, testtracer.WithRequestDelay(500*time.Millisecond))

		ticker := time.NewTicker(10 * time.Microsecond)
		defer ticker.Stop()

		done := make(chan struct{})

		// force flushes to test if StartSpan gets blocked while the tracer is sending payloads
		go func() {
			for {
				select {
				case <-ticker.C:
					ll.Flush()

				case <-done:
					return
				}
			}
		}()

		run(b, ll, tt, done)
	})
}

func testTracer(t testing.TB, opts ...testtracer.Option) (*testtracer.TestTracer, *llmobs.LLMObs) {
	tOpts := append([]testtracer.Option{
		testtracer.WithTracerStartOpts(
			tracer.WithLLMObsEnabled(true),
			tracer.WithLLMObsMLApp(mlApp),
			tracer.WithLogStartup(false),
		),
		testtracer.WithAgentInfoResponse(testtracer.AgentInfo{
			Endpoints: []string{"/evp_proxy/v2/"},
		}),
	}, opts...)

	tt := testtracer.Start(t, tOpts...)
	t.Cleanup(tt.Stop)

	ll, err := llmobs.ActiveLLMObs()
	require.NoError(t, err)

	return tt, ll
}

func findTag(tags []string, name string) string {
	for _, t := range tags {
		parts := strings.Split(t, ":")
		if len(parts) != 2 {
			continue
		}
		if parts[0] == name {
			return parts[1]
		}
	}
	return ""
}

func testClientServer(t *testing.T, h http.Handler) (*httptest.Server, *http.Client) {
	wh := traceHandler(h)
	srv := httptest.NewServer(wh)
	cl := traceClient(srv.Client())
	t.Cleanup(srv.Close)

	return srv, cl
}

func traceHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()

		opts := []tracer.StartSpanOption{
			tracer.Tag("span.kind", "server"),
		}
		parentCtx, err := tracer.Extract(tracer.HTTPHeadersCarrier(req.Header))
		if err == nil && parentCtx != nil {
			opts = append(opts, tracer.ChildOf(parentCtx))
		}

		span, ctx := tracer.StartSpanFromContext(ctx, "http.request", opts...)
		defer span.Finish()

		h.ServeHTTP(w, req.WithContext(ctx))
	})
}

func traceClient(c *http.Client) *http.Client {
	c.Transport = &tracedRT{base: c.Transport}
	return c
}

type tracedRT struct {
	base http.RoundTripper
}

func (rt *tracedRT) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	span, ctx := tracer.StartSpanFromContext(ctx, "http.request", tracer.Tag("span.kind", "client"))
	defer span.Finish()

	// Clone the request so we can modify it without causing visible side-effects to the caller...
	req = req.Clone(ctx)
	err := tracer.Inject(span.Context(), tracer.HTTPHeadersCarrier(req.Header))
	if err != nil {
		fmt.Fprintf(os.Stderr, "contrib/net/http.Roundtrip: failed to inject http headers: %s\n", err.Error())
	}

	return rt.base.RoundTrip(req)
}

func ptrFromVal[T any](v T) *T {
	return &v
}

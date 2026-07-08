// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package genaitrace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils/testtracer"
)

func testTracer(t *testing.T) *testtracer.TestTracer {
	tt := testtracer.Start(t,
		testtracer.WithTracerStartOpts(
			tracer.WithLLMObsEnabled(true),
			tracer.WithLLMObsMLApp("test-genai-app"),
			tracer.WithLogStartup(false),
		),
		testtracer.WithAgentInfoResponse(testtracer.AgentInfo{
			Endpoints: []string{"/evp_proxy/v2/"},
		}),
	)
	t.Cleanup(tt.Stop)
	return tt
}

// mockServer serves any path with the same canned JSON payload and supports
// SSE streaming when ?alt=sse is present.
func mockServer(t *testing.T, payload any, sseChunks []any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("alt") == "sse" || strings.Contains(r.URL.RawQuery, "alt=sse") {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			for _, c := range sseChunks {
				b, _ := json.Marshal(c)
				_, _ = w.Write([]byte("data: "))
				_, _ = w.Write(b)
				_, _ = w.Write([]byte("\n\n"))
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
}

func newTestClient(t *testing.T, baseURL string) *genai.Client {
	t.Helper()
	c, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:  "test-key",
		Backend: genai.BackendGeminiAPI,
		HTTPOptions: genai.HTTPOptions{
			BaseURL: baseURL,
		},
	})
	require.NoError(t, err)
	return c
}

func TestGenerateContent(t *testing.T) {
	tt := testTracer(t)

	payload := map[string]any{
		"candidates": []map[string]any{
			{
				"content": map[string]any{
					"role":  "model",
					"parts": []map[string]any{{"text": "Hello world!"}},
				},
				"finishReason": "STOP",
				"index":        0,
			},
		},
		"usageMetadata": map[string]any{
			"promptTokenCount":     7,
			"candidatesTokenCount": 3,
			"totalTokenCount":      10,
		},
		"modelVersion": "gemini-2.0-flash",
	}
	srv := mockServer(t, payload, nil)
	defer srv.Close()

	client := WrapClient(newTestClient(t, srv.URL))

	resp, err := client.Models.GenerateContent(
		context.Background(),
		"gemini-2.0-flash",
		[]*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
		nil,
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "Hello world!", resp.Text())

	spans := tt.WaitForLLMObsSpans(t, 1)
	require.Len(t, spans, 1)

	s := spans[0]
	assert.Equal(t, "genai.generate_content", s.Name)
	assert.Equal(t, "llm", s.Meta["span.kind"])
	assert.Equal(t, "gemini-2.0-flash", s.Meta["model_name"])
	assert.Equal(t, "google", s.Meta["model_provider"])

	require.Contains(t, s.Metrics, "input_tokens")
	require.Contains(t, s.Metrics, "output_tokens")
	require.Contains(t, s.Metrics, "total_tokens")
	assert.EqualValues(t, 7, s.Metrics["input_tokens"])
	assert.EqualValues(t, 3, s.Metrics["output_tokens"])
	assert.EqualValues(t, 10, s.Metrics["total_tokens"])

	require.Contains(t, s.Meta, "input")
	require.Contains(t, s.Meta, "output")
	inputJSON, _ := json.Marshal(s.Meta["input"])
	outputJSON, _ := json.Marshal(s.Meta["output"])
	assert.Contains(t, string(inputJSON), "hi")
	assert.Contains(t, string(inputJSON), "user")
	assert.Contains(t, string(outputJSON), "Hello world!")
	assert.Contains(t, string(outputJSON), "assistant")
}

func TestGenerateContentStream(t *testing.T) {
	tt := testTracer(t)

	chunks := []any{
		map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{"role": "model", "parts": []map[string]any{{"text": "Hello "}}},
				"index":   0,
			}},
		},
		map[string]any{
			"candidates": []map[string]any{{
				"content":      map[string]any{"role": "model", "parts": []map[string]any{{"text": "world!"}}},
				"finishReason": "STOP",
				"index":        0,
			}},
			"usageMetadata": map[string]any{
				"promptTokenCount":     2,
				"candidatesTokenCount": 2,
				"totalTokenCount":      4,
			},
		},
	}
	srv := mockServer(t, nil, chunks)
	defer srv.Close()

	client := WrapClient(newTestClient(t, srv.URL))

	var collected strings.Builder
	for chunk, err := range client.Models.GenerateContentStream(
		context.Background(),
		"gemini-2.0-flash",
		[]*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
		nil,
	) {
		require.NoError(t, err)
		if chunk != nil {
			collected.WriteString(chunk.Text())
		}
	}
	assert.Equal(t, "Hello world!", collected.String())

	spans := tt.WaitForLLMObsSpans(t, 1)
	require.Len(t, spans, 1)
	s := spans[0]
	assert.Equal(t, "genai.generate_content_stream", s.Name)
	outputJSON, _ := json.Marshal(s.Meta["output"])
	assert.Contains(t, string(outputJSON), "Hello world!")
	assert.EqualValues(t, 4, s.Metrics["total_tokens"])
}

func TestEmbedContent(t *testing.T) {
	tt := testTracer(t)

	payload := map[string]any{
		"embeddings": []map[string]any{
			{"values": []float32{0.1, 0.2, 0.3}},
		},
	}
	srv := mockServer(t, payload, nil)
	defer srv.Close()

	client := WrapClient(newTestClient(t, srv.URL))

	resp, err := client.Models.EmbedContent(
		context.Background(),
		"text-embedding-004",
		[]*genai.Content{{Parts: []*genai.Part{{Text: "embed me"}}}},
		nil,
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, resp.Embeddings, 1)
	assert.Len(t, resp.Embeddings[0].Values, 3)

	spans := tt.WaitForLLMObsSpans(t, 1)
	require.Len(t, spans, 1)
	s := spans[0]
	assert.Equal(t, "genai.embed_content", s.Name)
	assert.Equal(t, "embedding", s.Meta["span.kind"])
	assert.Equal(t, "text-embedding-004", s.Meta["model_name"])
	inputJSON, _ := json.Marshal(s.Meta["input"])
	outputJSON, _ := json.Marshal(s.Meta["output"])
	assert.Contains(t, string(inputJSON), "embed me")
	assert.Contains(t, string(outputJSON), "3 floats")
}

func TestChatSendMessage(t *testing.T) {
	tt := testTracer(t)

	payload := map[string]any{
		"candidates": []map[string]any{
			{
				"content": map[string]any{
					"role":  "model",
					"parts": []map[string]any{{"text": "Hi back!"}},
				},
				"finishReason": "STOP",
				"index":        0,
			},
		},
		"usageMetadata": map[string]any{
			"promptTokenCount":     5,
			"candidatesTokenCount": 2,
			"totalTokenCount":      7,
		},
	}
	srv := mockServer(t, payload, nil)
	defer srv.Close()

	client := WrapClient(newTestClient(t, srv.URL))

	chat, err := client.Chats.Create(context.Background(), "gemini-2.0-flash", nil, []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "previous question"}}},
		{Role: "model", Parts: []*genai.Part{{Text: "previous answer"}}},
	})
	require.NoError(t, err)

	resp, err := chat.SendMessage(context.Background(), genai.Part{Text: "Hello!"})
	require.NoError(t, err)
	assert.Equal(t, "Hi back!", resp.Text())

	// Second send: ensures we snapshot history *before* the call, since
	// genai.Chat appends the just-sent turn to History on success.
	resp, err = chat.SendMessage(context.Background(), genai.Part{Text: "Follow up?"})
	require.NoError(t, err)
	assert.Equal(t, "Hi back!", resp.Text())

	spans := tt.WaitForLLMObsSpans(t, 2)
	require.Len(t, spans, 2)

	for _, s := range spans {
		assert.Equal(t, "genai.chat.send_message", s.Name)
		assert.Equal(t, "gemini-2.0-flash", s.Meta["model_name"])
	}

	// First-call span: history + first user message only. The assistant
	// reply for this turn must NOT appear in this turn's recorded input.
	inputJSON, _ := json.Marshal(spans[0].Meta["input"])
	in := string(inputJSON)
	assert.Contains(t, in, "previous question")
	assert.Contains(t, in, "previous answer")
	assert.Contains(t, in, "Hello!")
	assert.NotContains(t, in, "Follow up?")
	assert.Equal(t, 0, strings.Count(in, "Hi back!"))
	assert.Equal(t, 1, strings.Count(in, "Hello!"))

	// Second-call span: includes the first completed turn (user + assistant)
	// plus the new user message, with no duplicated user content.
	inputJSON2, _ := json.Marshal(spans[1].Meta["input"])
	in2 := string(inputJSON2)
	assert.Contains(t, in2, "Hello!")
	assert.Contains(t, in2, "Follow up?")
	assert.Equal(t, 1, strings.Count(in2, "Hello!"))
	assert.Equal(t, 1, strings.Count(in2, "Follow up?"))
	assert.Equal(t, 1, strings.Count(in2, "Hi back!"))

	outputJSON, _ := json.Marshal(spans[0].Meta["output"])
	assert.Contains(t, string(outputJSON), "Hi back!")
	assert.EqualValues(t, 7, spans[0].Metrics["total_tokens"])
}

func TestGenerateContentError(t *testing.T) {
	tt := testTracer(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
	}))
	defer srv.Close()

	client := WrapClient(newTestClient(t, srv.URL))
	_, err := client.Models.GenerateContent(
		context.Background(),
		"gemini-2.0-flash",
		[]*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
		nil,
	)
	require.Error(t, err)

	spans := tt.WaitForLLMObsSpans(t, 1)
	require.Len(t, spans, 1)
	s := spans[0]
	assert.Contains(t, s.Meta, "error.message")
}

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package genaitrace

import (
	"context"
	"iter"

	"google.golang.org/genai"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/llmobs"
)

const integration = string(instrumentation.PackageGoogleAPIsGoGenAI)

// Models wraps *genai.Client.Models with LLM Observability tracing.
type Models struct {
	m        *genai.Models
	provider string
}

// GenerateContent generates content from the model and emits an LLM span.
func (m *Models) GenerateContent(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
	if m == nil || m.m == nil {
		return nil, errNilClient
	}
	span, ctx := startLLMSpan(ctx, "genai.generate_content", model, m.provider)

	resp, err := m.m.GenerateContent(ctx, model, contents, config)
	finishLLMSpan(span, contents, config, resp, err)
	return resp, err
}

// GenerateContentStream streams content from the model and emits an LLM span
// covering the entire stream. The span is finished when the returned iterator
// is fully consumed (or the caller stops iterating).
func (m *Models) GenerateContentStream(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) iter.Seq2[*genai.GenerateContentResponse, error] {
	return func(yield func(*genai.GenerateContentResponse, error) bool) {
		if m == nil || m.m == nil {
			yield(nil, errNilClient)
			return
		}
		span, ctx := startLLMSpan(ctx, "genai.generate_content_stream", model, m.provider)

		acc := newStreamAccumulator()
		var lastErr error
		for chunk, err := range m.m.GenerateContentStream(ctx, model, contents, config) {
			if err != nil {
				lastErr = err
			} else {
				acc.add(chunk)
			}
			if !yield(chunk, err) {
				break
			}
		}
		finishLLMSpan(span, contents, config, acc.response(), lastErr)
	}
}

// EmbedContent generates an embedding from the model and emits an embedding span.
func (m *Models) EmbedContent(ctx context.Context, model string, contents []*genai.Content, config *genai.EmbedContentConfig) (*genai.EmbedContentResponse, error) {
	if m == nil || m.m == nil {
		return nil, errNilClient
	}
	span, ctx := llmobs.StartEmbeddingSpan(ctx, "genai.embed_content",
		llmobs.WithModelName(model),
		llmobs.WithModelProvider(m.provider),
		llmobs.WithIntegration(integration),
	)

	resp, err := m.m.EmbedContent(ctx, model, contents, config)
	finishEmbeddingSpan(span, contents, resp, err)
	return resp, err
}

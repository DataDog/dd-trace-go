// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package genaitrace

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/genai"

	"github.com/DataDog/dd-trace-go/v2/llmobs"
)

var errNilClient = errors.New("genaitrace: wrapped client is nil")

func startLLMSpan(ctx context.Context, name, model, provider string) (*llmobs.LLMSpan, context.Context) {
	return llmobs.StartLLMSpan(ctx, name,
		llmobs.WithModelName(model),
		llmobs.WithModelProvider(provider),
		llmobs.WithIntegration(integration),
	)
}

// finishLLMSpan annotates and finishes an LLM span with input/output messages,
// generation config metadata, token usage metrics and any error.
func finishLLMSpan(span *llmobs.LLMSpan, contents []*genai.Content, config *genai.GenerateContentConfig, resp *genai.GenerateContentResponse, err error) {
	annotateLLMIO(span, contents, config, resp)

	if err != nil {
		span.Finish(llmobs.WithError(err))
		return
	}
	span.Finish()
}

func annotateLLMIO(span *llmobs.LLMSpan, contents []*genai.Content, config *genai.GenerateContentConfig, resp *genai.GenerateContentResponse) {
	input := contentsToMessages(contents, "user")
	if config != nil && config.SystemInstruction != nil {
		sys := contentsToMessages([]*genai.Content{config.SystemInstruction}, "system")
		input = append(sys, input...)
	}

	var output []llmobs.LLMMessage
	if resp != nil {
		for _, cand := range resp.Candidates {
			if cand == nil || cand.Content == nil {
				continue
			}
			output = append(output, contentToMessage(cand.Content, "assistant"))
		}
	}

	opts := []llmobs.AnnotateOption{}
	if meta := generationMetadata(config); len(meta) > 0 {
		opts = append(opts, llmobs.WithAnnotatedMetadata(meta))
	}
	if metrics := usageMetrics(resp); len(metrics) > 0 {
		opts = append(opts, llmobs.WithAnnotatedMetrics(metrics))
	}
	span.AnnotateLLMIO(input, output, opts...)
}

func generationMetadata(config *genai.GenerateContentConfig) map[string]any {
	if config == nil {
		return nil
	}
	meta := map[string]any{}
	if config.Temperature != nil {
		meta["temperature"] = *config.Temperature
	}
	if config.TopP != nil {
		meta["top_p"] = *config.TopP
	}
	if config.TopK != nil {
		meta["top_k"] = *config.TopK
	}
	if config.MaxOutputTokens != 0 {
		meta["max_output_tokens"] = config.MaxOutputTokens
	}
	if len(config.StopSequences) > 0 {
		meta["stop_sequences"] = config.StopSequences
	}
	if len(meta) == 0 {
		return nil
	}
	return meta
}

func usageMetrics(resp *genai.GenerateContentResponse) map[string]float64 {
	if resp == nil || resp.UsageMetadata == nil {
		return nil
	}
	u := resp.UsageMetadata
	metrics := map[string]float64{}
	if u.PromptTokenCount != 0 {
		metrics[llmobs.MetricKeyInputTokens] = float64(u.PromptTokenCount)
	}
	if u.CandidatesTokenCount != 0 {
		metrics[llmobs.MetricKeyOutputTokens] = float64(u.CandidatesTokenCount)
	}
	if u.TotalTokenCount != 0 {
		metrics[llmobs.MetricKeyTotalTokens] = float64(u.TotalTokenCount)
	}
	if u.CachedContentTokenCount != 0 {
		metrics[llmobs.MetricKeyCacheReadInputTokens] = float64(u.CachedContentTokenCount)
	}
	if u.ThoughtsTokenCount != 0 {
		metrics[llmobs.MetricKeyReasoningOutputTokens] = float64(u.ThoughtsTokenCount)
	}
	if len(metrics) == 0 {
		return nil
	}
	return metrics
}

func contentsToMessages(contents []*genai.Content, defaultRole string) []llmobs.LLMMessage {
	out := make([]llmobs.LLMMessage, 0, len(contents))
	for _, c := range contents {
		if c == nil {
			continue
		}
		out = append(out, contentToMessage(c, defaultRole))
	}
	return out
}

func contentToMessage(c *genai.Content, defaultRole string) llmobs.LLMMessage {
	role := c.Role
	if role == "" {
		role = defaultRole
	} else if role == string(genai.RoleModel) {
		role = "assistant"
	}
	msg := llmobs.LLMMessage{Role: role}

	var textParts []string
	for _, p := range c.Parts {
		if p == nil {
			continue
		}
		if p.Text != "" {
			textParts = append(textParts, p.Text)
		}
		if fc := p.FunctionCall; fc != nil {
			args, _ := jsonRaw(fc.Args)
			msg.ToolCalls = append(msg.ToolCalls, llmobs.ToolCall{
				Name:      fc.Name,
				Arguments: args,
				ToolID:    fc.ID,
				Type:      "function",
			})
		}
		if fr := p.FunctionResponse; fr != nil {
			msg.ToolResults = append(msg.ToolResults, llmobs.ToolResult{
				Name:   fr.Name,
				Result: fr.Response,
				ToolID: fr.ID,
				Type:   "function",
			})
		}
	}
	msg.Content = strings.Join(textParts, "")
	return msg
}

func finishEmbeddingSpan(span *llmobs.EmbeddingSpan, contents []*genai.Content, resp *genai.EmbedContentResponse, err error) {
	docs := embedDocs(contents)
	output := embedSummary(resp)
	span.AnnotateEmbeddingIO(docs, output)

	if err != nil {
		span.Finish(llmobs.WithError(err))
		return
	}
	span.Finish()
}

func embedDocs(contents []*genai.Content) []llmobs.EmbeddedDocument {
	out := make([]llmobs.EmbeddedDocument, 0, len(contents))
	for _, c := range contents {
		if c == nil {
			continue
		}
		var parts []string
		for _, p := range c.Parts {
			if p != nil && p.Text != "" {
				parts = append(parts, p.Text)
			}
		}
		out = append(out, llmobs.EmbeddedDocument{Text: strings.Join(parts, "")})
	}
	return out
}

func embedSummary(resp *genai.EmbedContentResponse) string {
	if resp == nil || len(resp.Embeddings) == 0 {
		return ""
	}
	var b strings.Builder
	for i, e := range resp.Embeddings {
		if i > 0 {
			b.WriteString(", ")
		}
		if e == nil {
			b.WriteString("[]")
			continue
		}
		b.WriteString("[")
		b.WriteString(itoa(len(e.Values)))
		b.WriteString(" floats]")
	}
	return b.String()
}

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package llmobs_test

import (
	"context"
	"log"

	"github.com/DataDog/dd-trace-go/v2/llmobs"
)

func ExampleStartLLMSpan() {
	ctx := context.Background()
	span, ctx := llmobs.StartLLMSpan(ctx, "llm-span", llmobs.WithMLApp("ml-app"), llmobs.WithModelName("model_name"), llmobs.WithModelProvider("model_provider"))
	defer span.Finish()

	input := []llmobs.LLMMessage{
		{
			Role:    "user",
			Content: "Hello world!",
		},
	}
	output := []llmobs.LLMMessage{
		{
			Role:    "assistant",
			Content: "How can I help?",
		},
	}
	span.AnnotateLLMIO(
		input,
		output,
		llmobs.WithAnnotatedMetadata(map[string]any{"temperature": 0, "max_tokens": 200}),
		llmobs.WithAnnotatedTags(map[string]string{"host": "host_name"}),
		llmobs.WithAnnotatedMetrics(map[string]float64{llmobs.MetricKeyInputTokens: 4, llmobs.MetricKeyOutputTokens: 6, llmobs.MetricKeyTotalTokens: 10}),
	)
}

func ExampleSpanFromContext() {
	ctx := context.Background()
	_, ctx = llmobs.StartLLMSpan(ctx, "llm-span", llmobs.WithMLApp("ml-app"), llmobs.WithModelName("model_name"), llmobs.WithModelProvider("model_provider"))

	span, ok := llmobs.SpanFromContext(ctx)
	if !ok {
		log.Fatal("span not found in context")
	}
	llm, ok := span.AsLLM()
	if !ok {
		log.Fatal("span was not llm")
	}
	llm.Finish()
}

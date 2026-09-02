// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package llmobs_test

import (
	"context"

	"github.com/DataDog/dd-trace-go/v2/llmobs"
)

func ExampleGetPrompt() {
	prompt, err := llmobs.GetPrompt(context.Background(), "greeting",
		llmobs.WithPromptTargetingKey("user-123"),
		llmobs.WithPromptTargetingAttributes(map[string]any{"tier": "premium"}),
		llmobs.WithPromptFallback(llmobs.PromptFallback{Template: llmobs.PromptTemplate{Text: "Hello {name}"}}),
	)
	if err != nil {
		return
	}
	variables := map[string]any{"name": "Ada"}
	_ = prompt.Format(variables)
	span, _ := llmobs.StartLLMSpan(context.Background(), "request")
	span.Annotate(llmobs.WithAnnotatedPrompt(prompt.Annotation(variables)))
}

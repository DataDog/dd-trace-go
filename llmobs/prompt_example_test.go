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
	ctx := context.Background() // In an HTTP handler, use the request's context.
	prompt, err := llmobs.GetPrompt(ctx, "greeting",
		llmobs.WithPromptTargetingKey("user-123"),
		llmobs.WithPromptTargetingAttributes(map[string]any{"tier": "premium"}),
		llmobs.WithPromptFallback(llmobs.PromptFallback{Template: llmobs.PromptTemplate{Text: "Hello {name}"}}),
	)
	if err != nil {
		return
	}
	variables := map[string]any{"name": "Ada"}
	rendered := prompt.Format(variables)
	span, _ := llmobs.StartLLMSpan(ctx, "request")
	defer span.Finish()
	span.Annotate(llmobs.WithAnnotatedPrompt(prompt.Annotation(variables)))
	_ = rendered.Text // Pass this to the model client.
}

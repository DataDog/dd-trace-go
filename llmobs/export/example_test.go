// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package export_test

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/DataDog/dd-trace-go/v2/llmobs/export"
)

// Export a batch of spans reconstructed offline. An empty site or API key falls
// back to DD_SITE and DD_API_KEY.
func ExampleClient_SubmitSpans() {
	client, err := export.NewClient("my-ml-app",
		export.WithDatadogIntake("datadoghq.com", "<api-key>"),
		export.WithService("my-service"),
		export.WithEnv("prod"),
	)
	if err != nil {
		log.Fatal(err)
	}

	res, err := client.SubmitSpans(context.Background(), []export.SpanEvent{{
		TraceID:       "1234567890",
		SpanID:        "2345678901",
		Kind:          export.KindLLM,
		Name:          "chat",
		Start:         time.Now().Add(-2 * time.Second),
		Duration:      int64(1500 * time.Millisecond),
		ModelName:     "gpt-4o",
		ModelProvider: "openai",
		Input:         "hello",
		Output:        "hi there",
		Metrics:       map[string]float64{"input_tokens": 12, "output_tokens": 8},
	}})
	if err != nil {
		// err is non-nil if any request failed; res still carries per-row detail.
		log.Print(err)
	}

	fmt.Println(res.Sent, res.Dropped, res.Failed)
	for _, ve := range res.ValidationErrors {
		fmt.Println(ve.Index, ve.Code, ve.Reason)
	}
}

// Attach an evaluation to a span exported earlier, joining by span ID.
func ExampleClient_SubmitEvaluations() {
	client, err := export.NewClient("my-ml-app",
		export.WithAgentURL("http://localhost:8126"),
	)
	if err != nil {
		log.Fatal(err)
	}

	score := 0.87
	res, err := client.SubmitEvaluations(context.Background(), []export.EvaluationMetric{{
		SpanID:     "2345678901",
		TraceID:    "1234567890",
		Label:      "answer_quality",
		ScoreValue: &score,
		Assessment: "mostly correct",
		Reasoning:  "cited two of three sources",
	}})
	if err != nil {
		log.Print(err)
	}

	fmt.Println(res.Sent, res.Dropped, res.Failed)
}

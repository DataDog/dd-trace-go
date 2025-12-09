// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package dataset_test

import (
	"context"
	"log"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/llmobs/dataset"
)

func ExampleCreate() {
	if err := tracer.Start(tracer.WithLLMObsEnabled(true)); err != nil {
		log.Fatal(err)
	}
	defer tracer.Stop()

	ctx := context.Background()

	// First, create the dataset
	ds, err := dataset.Create(
		ctx,
		"capitals-of-the-world",
		[]dataset.Record{
			{
				Input: map[string]any{
					"question": "What is the capital of China?",
				},
				ExpectedOutput: "Beijing",
				Metadata: map[string]any{
					"difficulty": "easy",
				},
			},
			{
				Input: map[string]any{
					"question": "Which city serves as the capital of South Africa?",
				},
				ExpectedOutput: "Pretoria",
				Metadata: map[string]any{
					"difficulty": "medium",
				},
			},
		},
		dataset.WithDescription("Questions about world capitals"),
	)
	if err != nil {
		log.Fatal(err)
	}

	// Finally, push the dataset to DataDog
	if err := ds.Push(ctx); err != nil {
		log.Fatal(err)
	}
}

func ExampleDataset_Append() {
	if err := tracer.Start(tracer.WithLLMObsEnabled(true)); err != nil {
		log.Fatal(err)
	}
	defer tracer.Stop()

	ctx := context.Background()

	// First, create the dataset
	ds, err := dataset.Create(
		ctx,
		"capitals-of-the-world",
		[]dataset.Record{
			{
				Input: map[string]any{
					"question": "What is the capital of China?",
				},
				ExpectedOutput: "Beijing",
				Metadata: map[string]any{
					"difficulty": "easy",
				},
			},
			{
				Input: map[string]any{
					"question": "Which city serves as the capital of South Africa?",
				},
				ExpectedOutput: "Pretoria",
				Metadata: map[string]any{
					"difficulty": "medium",
				},
			},
		},
		dataset.WithDescription("Questions about world capitals"),
	)
	if err != nil {
		log.Fatal(err)
	}

	// Add a new question
	ds.Append(dataset.Record{
		Input: map[string]any{
			"question": "What is the capital of China?",
		},
		ExpectedOutput: "Beijing",
		Metadata: map[string]any{
			"difficulty": "easy",
		},
	})

	// Finally, push the dataset to DataDog
	if err := ds.Push(ctx); err != nil {
		log.Fatal(err)
	}
}

func ExampleDataset_Update() {
	if err := tracer.Start(tracer.WithLLMObsEnabled(true)); err != nil {
		log.Fatal(err)
	}
	defer tracer.Stop()

	ctx := context.Background()

	// First, create the dataset
	ds, err := dataset.Create(
		ctx,
		"capitals-of-the-world",
		[]dataset.Record{
			{
				Input: map[string]any{
					"question": "What is the capital of China?",
				},
				ExpectedOutput: "Beijing",
				Metadata: map[string]any{
					"difficulty": "easy",
				},
			},
			{
				Input: map[string]any{
					"question": "Which city serves as the capital of South Africa?",
				},
				ExpectedOutput: "Pretoria",
				Metadata: map[string]any{
					"difficulty": "medium",
				},
			},
		},
		dataset.WithDescription("Questions about world capitals"),
	)
	if err != nil {
		log.Fatal(err)
	}

	// Update the difficulty of the first question
	ds.Update(0, dataset.RecordUpdate{
		Input:          nil, // leave unchanged
		ExpectedOutput: nil, // leave unchanged
		Metadata:       map[string]any{"difficulty": "medium"},
	})

	// Finally, push the dataset to DataDog
	if err := ds.Push(ctx); err != nil {
		log.Fatal(err)
	}
}

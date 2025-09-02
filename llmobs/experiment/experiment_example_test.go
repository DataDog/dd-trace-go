// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package experiment_test

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/llmobs/dataset"
	"github.com/DataDog/dd-trace-go/v2/llmobs/experiment"
)

func ExampleNew() {
	ds, err := dataset.Pull(context.TODO(), "capitals-of-the-world")
	if err != nil {
		log.Fatal(err)
	}

	task := experiment.Task(func(ctx context.Context, inputData map[string]any, experimentCfg map[string]any) (any, error) {
		question := inputData["question"].(string)
		// Your LLM or processing logic here
		if strings.Contains(question, "China") {
			return "Beijing", nil
		}
		return "Unknown", nil
	})

	evs := []experiment.Evaluator{
		experiment.NewEvaluator("exact-match", func(input map[string]any, output any, expectedOutput any) (any, error) {
			return output == expectedOutput, nil
		}),
		experiment.NewEvaluator("overlap", func(input map[string]any, output any, expectedOutput any) (any, error) {
			outStr, ok := output.(string)
			if !ok {
				return nil, fmt.Errorf("wanted output to be a string, got: %T", output)
			}
			expStr, ok := expectedOutput.(string)
			if !ok {
				return nil, fmt.Errorf("wanted expectedOutput to be a string, got: %T", expectedOutput)
			}

			outSet := make(map[rune]struct{})
			for _, r := range outStr {
				outSet[r] = struct{}{}
			}
			expSet := make(map[rune]struct{})
			for _, r := range expStr {
				expSet[r] = struct{}{}
			}

			// Intersection size
			intersection := 0
			for r := range outSet {
				if _, ok := expSet[r]; ok {
					intersection++
				}
			}
			// |A ∪ B| = |A| + |B| − |A ∩ B|
			union := len(outSet) + len(expSet) - intersection

			// Jaccard similarity. Define both-empty as a perfect match.
			var score float64
			if union == 0 {
				score = 1.0
			} else {
				score = float64(intersection) / float64(union)
			}

			return score, nil
		}),
		experiment.NewEvaluator("fake-llm-as-a-judge", func(input map[string]any, output any, expectedOutput any) (any, error) {
			return "excellent", nil
		}),
	}

	exp, err := experiment.New(
		"my-experiment",
		task,
		ds,
		evs,
		"Testing capital cities knowledge",
		experiment.WithExperimentConfig(
			map[string]any{
				"model_name": "gpt-4",
				"version":    "1.0",
			},
		),
	)
	if err != nil {
		log.Fatal(err)
	}

	results, err := exp.Run(context.TODO())
	if err != nil {
		log.Fatal(err)
	}

	for _, res := range results {
		fmt.Printf("Record: %d", res.RecordIndex)
		fmt.Printf("Input: %v", res.Input)
		fmt.Printf("Output: %v", res.Output)
		for _, ev := range res.Evaluations {
			fmt.Printf("Evaluator score (%s): %v", ev.Name, ev.Value)
			if ev.Error != nil {
				fmt.Printf("Evaluator error (%s): %v", ev.Name, ev.Error)
			}
		}
		if res.Error != nil {
			fmt.Printf("Error: %v", res.Error)
		}
	}
}

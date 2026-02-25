// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// This example starts a devserver on :8787 with a simple capital-city experiment.
// It requires a running Datadog agent or agentless configuration (DD_API_KEY, DD_APP_KEY).
//
// Usage:
//
//	go run ./llmobs/experiment/devserver/examples/
package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/llmobs/dataset"
	"github.com/DataDog/dd-trace-go/v2/llmobs/experiment"
	"github.com/DataDog/dd-trace-go/v2/llmobs/experiment/devserver"
)

func main() {
	if err := tracer.Start(
		tracer.WithLLMObsEnabled(true),
		tracer.WithLLMObsMLApp("devserver-example"),
		tracer.WithLLMObsProjectName("devserver-example"),
		tracer.WithService("devserver-example"),
	); err != nil {
		fmt.Fprintf(os.Stderr, "failed to start tracer: %v\n", err)
		os.Exit(1)
	}
	defer tracer.Stop()

	// Pull the dataset if it already exists, otherwise create it.
	ds, err := dataset.Pull(context.Background(), "capitals")
	if err != nil {
		ds, err = dataset.Create(context.Background(), "capitals", []dataset.Record{
			{Input: map[string]any{"question": "What is the capital of France?"}, ExpectedOutput: "Paris"},
			{Input: map[string]any{"question": "What is the capital of Germany?"}, ExpectedOutput: "Berlin"},
			{Input: map[string]any{"question": "What is the capital of Japan?"}, ExpectedOutput: "Tokyo"},
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to create dataset: %v\n", err)
			os.Exit(1)
		}
	}

	task := experiment.NewTask("capital-answerer", func(ctx context.Context, rec dataset.Record, cfg map[string]any) (any, error) {
		q := rec.Input.(map[string]any)["question"].(string)
		answers := map[string]string{
			"France":  "Paris",
			"Germany": "Berlin",
			"Japan":   "Tokyo",
		}

		// Read config: "accuracy" controls how often we return the correct answer (0.0-1.0).
		accuracy := 1.0
		if v, ok := cfg["accuracy"].(float64); ok {
			accuracy = v
		}

		// Read config: "prefix" is prepended to the answer.
		prefix := ""
		if v, ok := cfg["prefix"].(string); ok {
			prefix = v
		}

		// Read config: "system_prompt" controls the answer format.
		// This simulates how an LLM follows system prompt instructions.
		systemPrompt := "You are a geography expert. Answer with just the city name."
		if v, ok := cfg["system_prompt"].(string); ok {
			systemPrompt = v
		}

		var answer string
		for country, capital := range answers {
			if strings.Contains(q, country) {
				answer = capital
				break
			}
		}
		if answer == "" {
			answer = "Unknown"
		}

		// When accuracy < 1.0, randomly return a wrong answer.
		if accuracy < 1.0 && rand.Float64() >= accuracy {
			answer = "Wrong answer"
		}

		// Format the answer based on the system prompt.
		switch {
		case strings.Contains(systemPrompt, "full sentence"):
			// Find the country name for the full sentence format.
			country := "somewhere"
			for c := range answers {
				if strings.Contains(q, c) {
					country = c
					break
				}
			}
			answer = fmt.Sprintf("The capital of %s is %s.", country, answer)
		case !strings.Contains(systemPrompt, "just the city name"):
			// For custom prompts, include the prompt in brackets so it's visible.
			answer = fmt.Sprintf("[%s] %s", systemPrompt, answer)
		}

		return prefix + answer, nil
	})

	evaluators := []experiment.Evaluator{
		experiment.NewEvaluator("exact-match", func(ctx context.Context, rec dataset.Record, output any) (any, error) {
			return output == rec.ExpectedOutput, nil
		}),
		experiment.NewEvaluator("similarity", func(ctx context.Context, rec dataset.Record, output any) (any, error) {
			if output == rec.ExpectedOutput {
				return 1.0, nil
			}
			return 0.0, nil
		}),
		experiment.NewEvaluator("quality-label", func(ctx context.Context, rec dataset.Record, output any) (any, error) {
			if output == rec.ExpectedOutput {
				return "excellent", nil
			}
			return "poor", nil
		}),
	}

	srv := devserver.New(
		[]*devserver.ExperimentDefinition{{
			Name:        "capitals",
			Description: "Evaluate capital city question answering",
			ProjectName: "devserver-example",
			Task:        task,
			Dataset:     ds,
			Evaluators:  evaluators,
			Config: map[string]*devserver.ConfigField{
				"model": {
					Type:        devserver.ConfigFieldString,
					Default:     "gpt-3.5-turbo",
					Description: "LLM model to use",
					Choices:     []any{"gpt-3.5-turbo", "gpt-4", "claude-3-sonnet"},
				},
				"system_prompt": {
					Type:        devserver.ConfigFieldPrompt,
					Default:     "You are a geography expert. Answer with just the city name.",
					Description: "System prompt sent to the LLM",
				},
				"accuracy": {
					Type:        devserver.ConfigFieldNumber,
					Default:     1.0,
					Description: "Simulated correctness rate",
					Min:         ptr(0.0),
					Max:         ptr(1.0),
				},
				"prefix": {
					Type:        devserver.ConfigFieldString,
					Default:     "",
					Description: "Text prepended to every answer",
				},
			},
			Tags: map[string]string{"env": "dev"},
		}},
		devserver.WithAddr(":8787"),
	)

	fmt.Println("devserver listening on :8787")
	fmt.Println()
	fmt.Println("Try:")
	fmt.Println(`  curl http://localhost:8787/list`)
	fmt.Println(`  curl -X POST http://localhost:8787/eval -d '{"name":"capitals","stream":false}'`)
	fmt.Println(`  curl -X POST http://localhost:8787/eval -d '{"name":"capitals","stream":true}'`)
	fmt.Println(`  curl -X POST http://localhost:8787/eval -d '{"name":"capitals","stream":true,"config_override":{"accuracy":0.0}}'`)
	fmt.Println(`  curl -X POST http://localhost:8787/eval -d '{"name":"capitals","stream":false,"config_override":{"system_prompt":"Answer in a full sentence."}}'`)
	fmt.Println()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := srv.ListenAndServe(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}

func ptr(f float64) *float64 { return &f }

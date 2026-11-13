// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"testing"

	"github.com/DataDog/dd-trace-go/_eval/suites"
	"github.com/DataDog/dd-trace-go/v2/llmobs/dataset"
)

func TestDefaultModel(t *testing.T) {
	tests := []struct {
		agent string
		want  string
	}{
		{agent: "claude", want: "claude-sonnet-5"},
		{agent: "codex", want: "gpt-5.6-terra"},
	}
	for _, tt := range tests {
		t.Run(tt.agent, func(t *testing.T) {
			got, err := defaultModel(tt.agent)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("defaultModel(%q) = %q, want %q", tt.agent, got, tt.want)
			}
		})
	}

	if _, err := defaultModel("unknown"); err == nil {
		t.Fatal("defaultModel accepted an unknown agent")
	}
}

func TestSelectedAgents(t *testing.T) {
	all, err := selectedAgents("all")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0] != "claude" || all[1] != "codex" {
		t.Errorf("all agents = %v", all)
	}
	if codex, err := selectedAgents("codex"); err != nil || len(codex) != 1 || codex[0] != "codex" {
		t.Errorf("codex agents = %v, %v", codex, err)
	}
	listed, err := selectedAgents("codex,claude,codex")
	if err != nil || len(listed) != 2 || listed[0] != "codex" || listed[1] != "claude" {
		t.Errorf("listed agents = %v, %v", listed, err)
	}
	if mixed, err := selectedAgents("codex,all"); err != nil || len(mixed) != 2 {
		t.Errorf("mixed all agents = %v, %v", mixed, err)
	}
	if _, err := selectedAgents("unknown"); err == nil {
		t.Fatal("expected unknown agent to fail")
	}
}

func TestDatasetRecordIdentityAndEquality(t *testing.T) {
	input := map[string]any{
		"task_id":   "task",
		"prompt":    "do it",
		"prompt_id": "short",
	}
	rec := dataset.Record{Input: input}
	key, err := datasetRecordKey(rec)
	if err != nil {
		t.Fatal(err)
	}
	if key != "task\x00short" {
		t.Fatalf("key = %q", key)
	}

	pulled := dataset.Record{
		Input: map[string]any{
			"task_id":   "task",
			"prompt":    "do it",
			"prompt_id": "short",
		},
	}
	equal, err := sameDatasetRecord(rec, pulled)
	if err != nil {
		t.Fatal(err)
	}
	if !equal {
		t.Error("JSON-equivalent authored and pulled records should match")
	}
}

func TestDatasetRecordEqualityIgnoresObjectKeyOrder(t *testing.T) {
	type authoredInput struct {
		TaskID   string `json:"task_id"`
		PromptID string `json:"prompt_id"`
		Prompt   string `json:"prompt"`
	}
	authored := dataset.Record{
		Input: authoredInput{TaskID: "task", PromptID: "short", Prompt: "do it"},
		ExpectedOutput: struct {
			Validations map[string]bool `json:"validations"`
			ChecksTotal int             `json:"checks_total"`
		}{Validations: map[string]bool{"tests": true}, ChecksTotal: 1},
	}
	pulled := dataset.Record{
		Input: map[string]any{
			"prompt":    "do it",
			"prompt_id": "short",
			"task_id":   "task",
		},
		ExpectedOutput: map[string]any{
			"checks_total": 1,
			"validations":  map[string]any{"tests": true},
		},
	}
	equal, err := sameDatasetRecord(authored, pulled)
	if err != nil {
		t.Fatal(err)
	}
	if !equal {
		t.Error("semantically equal JSON records should match regardless of object key order")
	}
}

func TestSelectTasks(t *testing.T) {
	suite, err := suites.Lookup("integration-authoring")
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := selectTasks(suite, []string{"author-cloudevents-v2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Spec.TaskID != "author-cloudevents-v2" {
		t.Errorf("tasks = %+v", tasks)
	}
	if _, err := selectTasks(suite, []string{"missing"}); err == nil {
		t.Fatal("expected unknown task to fail")
	}
}

// TestAgentlessNoise pins which tracer messages the quiet logger drops. The
// filter has to stay narrow: a run with no agent produces the two suppressed
// messages on a loop, and every other tracer error still matters.
func TestAgentlessNoise(t *testing.T) {
	const prefix = "Datadog Tracer v2.11.0-dev.1 "
	tests := []struct {
		name    string
		msg     string
		silence bool
	}{
		{
			name:    "loading features",
			msg:     prefix + `ERROR: loading features: Get "http://localhost:8126/info": dial tcp [::1]:8126: connect: connection refused`,
			silence: true,
		},
		{
			name:    "lost one trace",
			msg:     prefix + `ERROR: lost 1 traces: Post "http://localhost:8126/v0.4/traces": dial tcp [::1]:8126: connect: connection refused`,
			silence: true,
		},
		{
			name:    "lost many traces",
			msg:     prefix + `ERROR: lost 42 traces: Post "http://localhost:8126/v0.4/traces": connection refused`,
			silence: true,
		},
		{
			name:    "rejected credentials must survive",
			msg:     prefix + "ERROR: llmobs: submitting spans: 403 Forbidden",
			silence: false,
		},
		{
			name:    "unreachable intake must survive",
			msg:     prefix + `ERROR: llmobs: Post "https://api.datadoghq.com/api/v2/llmobs": dial tcp: connection refused`,
			silence: false,
		},
		{
			name:    "dropped payload must survive",
			msg:     prefix + "ERROR: dropping payload: unsupported encoding",
			silence: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := agentlessNoise.MatchString(tt.msg); got != tt.silence {
				t.Fatalf("silence = %v, want %v for %q", got, tt.silence, tt.msg)
			}
		})
	}
}

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package agenteval

import "testing"

func testTask() Task {
	return Task{
		Spec: TaskSpec{
			TaskID:   "test-task",
			Prompt:   "default prompt",
			Mutation: Mutation{Kind: MutationNone, AssertAbsent: []string{"missing"}},
		},
	}
}

func TestTaskRecordsCreateOnePromptRecord(t *testing.T) {
	records, err := testTask().Records("test-suite")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	input, err := DecodeTaskInput(records[0])
	if err != nil {
		t.Fatal(err)
	}
	if input.PromptID != "default" || input.Prompt != "default prompt" {
		t.Errorf("input = %+v", input)
	}
}

func TestTaskRecordsCreatePromptVariants(t *testing.T) {
	task := testTask()
	task.Spec.Prompt = ""
	task.Prompts = []PromptVariant{
		{ID: "short", Prompt: "do it"},
		{ID: "specific", Prompt: "implement the feature"},
	}
	records, err := task.Records("test-suite")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	want := map[string]string{
		"short":    "do it",
		"specific": "implement the feature",
	}
	for _, rec := range records {
		input, err := DecodeTaskInput(rec)
		if err != nil {
			t.Fatal(err)
		}
		key := input.PromptID
		if input.Prompt != want[key] {
			t.Errorf("%s prompt = %q, want %q", key, input.Prompt, want[key])
		}
		delete(want, key)
	}
	if len(want) != 0 {
		t.Errorf("missing cases: %v", want)
	}
}

func TestTaskRecordsRejectDuplicatePromptIDs(t *testing.T) {
	task := testTask()
	task.Spec.Prompt = ""
	task.Prompts = []PromptVariant{
		{ID: "same", Prompt: "one"},
		{ID: "same", Prompt: "two"},
	}
	if _, err := task.Records("test-suite"); err == nil {
		t.Fatal("expected duplicate prompt IDs to fail")
	}
}

func TestTaskRecordExpectedOutputMatchesObservedShape(t *testing.T) {
	task := testTask()
	task.Spec.ValidationCommands = []ValidationCommand{{Label: "tests", Command: "go test ./..."}}
	task.Spec.ExpectedChangedPaths = []string{"contrib/example/"}
	records, err := task.Records("test-suite")
	if err != nil {
		t.Fatal(err)
	}
	expected, ok := records[0].ExpectedOutput.(ExpectedRunOutput)
	if !ok {
		t.Fatalf("expected output type = %T", records[0].ExpectedOutput)
	}
	if !expected.Validations["tests"] ||
		expected.ChecksTotal != len(expected.Checks) || expected.ChecksPassed != len(expected.Checks) || expected.ChecksScore != 1 ||
		expected.ValidationTotal != len(expected.Validations) || expected.ValidationPassed != len(expected.Validations) || expected.ValidationScore != 1 {
		t.Errorf("expected output = %+v", expected)
	}
	if len(expected.ChangedPaths) != 1 || expected.ChangedPaths[0] != "contrib/example/" {
		t.Errorf("changed paths = %v", expected.ChangedPaths)
	}
}

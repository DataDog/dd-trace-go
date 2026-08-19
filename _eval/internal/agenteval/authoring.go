// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package agenteval

import (
	"encoding/json"
	"fmt"

	"github.com/DataDog/dd-trace-go/v2/llmobs/dataset"
)

// TaskSize is a rough cost band. It drives how many runs a task is worth: small
// tasks are where the statistical sensitivity comes from, large ones are coarse
// end-to-end anchors that cost hours each.
type TaskSize string

const (
	SizeSmall TaskSize = "small"
	SizeLarge TaskSize = "large"
)

// TaskMetadata is the non-scored context attached to a record. It is what makes
// results sliceable in LLM Obs, so fill it in even though nothing enforces it.
type TaskMetadata struct {
	// Category groups tasks that exercise the same area, e.g. "integration_options".
	Category string `json:"category"`
	// FailureMode names the specific thing that goes wrong without the change
	// under test, e.g. "missing_aspect". One task, one failure mode.
	FailureMode string   `json:"failure_mode"`
	Size        TaskSize `json:"size"`
	// Suite is filled in by the suite the task belongs to.
	Suite string `json:"suite,omitempty"`
	// Source records where the task came from: a PR, an issue, an incident.
	// Review comments on real PRs are the best source of tasks there is, and
	// recording which one lets a reader check the criterion against it.
	Source string `json:"source,omitempty"`
}

// PromptVariant is one way to ask an agent to complete a task. Keeping the ID
// stable makes prompt wording directly comparable across experiment runs.
type PromptVariant struct {
	ID     string `json:"id"`
	Prompt string `json:"prompt"`
}

// Task is one eval task as its author writes it: a typed TaskSpec rather than
// the map[string]any a dataset record actually carries. Record converts.
//
// Authoring in Go types rather than maps is what makes a suite reviewable. A
// misspelled field is a compile error instead of a criterion that silently never
// fires.
type Task struct {
	Spec     TaskSpec
	Metadata TaskMetadata
	// Prompts replaces Spec.Prompt when a task should be tested with more than
	// one wording. If empty, Spec.Prompt becomes a single variant named default.
	Prompts []PromptVariant
}

// ExpectedRunOutput is the test-like expected result attached to a record.
type ExpectedRunOutput struct {
	Status           string             `json:"status"`
	ChangedPaths     []string           `json:"changed_paths,omitempty"`
	Validations      map[string]bool    `json:"validations,omitempty"`
	ValidationTotal  int                `json:"validation_total"`
	ValidationPassed int                `json:"validation_passed"`
	ValidationScore  float64            `json:"validation_score"`
	Checks           map[string]float64 `json:"checks,omitempty"`
	Diagnostics      map[string]bool    `json:"diagnostics,omitempty"`
	ChecksTotal      int                `json:"checks_total"`
	ChecksPassed     int                `json:"checks_passed"`
	ChecksScore      float64            `json:"checks_score"`
}

// Records creates one dataset point for each prompt.
func (t Task) Records(suite string) ([]dataset.Record, error) {
	prompts, err := t.promptVariants()
	if err != nil {
		return nil, err
	}

	records := make([]dataset.Record, 0, len(prompts))
	for _, prompt := range prompts {
		spec := t.Spec
		spec.Prompt = prompt.Prompt
		if err := spec.Validate(); err != nil {
			return nil, err
		}

		input := TaskInput{TaskID: spec.TaskID, PromptID: prompt.ID, Prompt: prompt.Prompt}
		meta := t.Metadata
		meta.Suite = suite
		metaMap, err := toMap(meta)
		if err != nil {
			return nil, fmt.Errorf("%s: encode metadata: %w", spec.TaskID, err)
		}
		metaMap["task_id"] = spec.TaskID
		metaMap["prompt_id"] = prompt.ID

		checks := expectedChecks(&spec)
		diagnostics := expectedDiagnostics(&spec)
		validations := expectedValidations(&spec)
		expected := ExpectedRunOutput{
			Status:           RunStatusCompleted,
			ChangedPaths:     spec.ExpectedChangedPaths,
			Validations:      validations,
			ValidationTotal:  len(validations),
			ValidationPassed: len(validations),
			ValidationScore:  perfectScore(len(validations)),
			Checks:           checks,
			Diagnostics:      diagnostics,
			ChecksTotal:      len(checks),
			ChecksPassed:     len(checks),
			ChecksScore:      perfectScore(len(checks)),
		}
		rec := dataset.Record{Input: input, ExpectedOutput: expected, Metadata: metaMap}
		if _, err := DecodeTaskInput(rec); err != nil {
			return nil, fmt.Errorf("%s: does not round-trip: %w", spec.TaskID, err)
		}
		records = append(records, rec)
	}
	return records, nil
}

func perfectScore(total int) float64 {
	if total == 0 {
		return 0
	}
	return 1
}

func expectedValidations(spec *TaskSpec) map[string]bool {
	if len(spec.ValidationCommands) == 0 {
		return nil
	}
	out := make(map[string]bool, len(spec.ValidationCommands))
	for _, validation := range spec.ValidationCommands {
		out[validation.Label] = true
	}
	return out
}

func (t Task) promptVariants() ([]PromptVariant, error) {
	if len(t.Prompts) == 0 {
		if t.Spec.Prompt == "" {
			return nil, fmt.Errorf("%s: prompt is required", t.Spec.TaskID)
		}
		return []PromptVariant{{ID: "default", Prompt: t.Spec.Prompt}}, nil
	}
	if t.Spec.Prompt != "" {
		return nil, fmt.Errorf("%s: set either spec prompt or prompt variants, not both", t.Spec.TaskID)
	}
	seen := make(map[string]struct{}, len(t.Prompts))
	for _, prompt := range t.Prompts {
		if prompt.ID == "" || prompt.Prompt == "" {
			return nil, fmt.Errorf("%s: prompt variant needs both id and prompt", t.Spec.TaskID)
		}
		if _, duplicate := seen[prompt.ID]; duplicate {
			return nil, fmt.Errorf("%s: duplicate prompt id %q", t.Spec.TaskID, prompt.ID)
		}
		seen[prompt.ID] = struct{}{}
	}
	return t.Prompts, nil
}

// toMap renders a value as the decoded-JSON shape records come back from
// dataset.Pull in, so pushing and pulling produce the same thing.
func toMap(v any) (map[string]any, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

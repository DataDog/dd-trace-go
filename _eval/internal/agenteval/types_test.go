// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package agenteval

import (
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/llmobs/dataset"
)

// validInput is the shape a record has after dataset.Pull decodes it: maps of any
// and slices of any, never []string.
func validInput() map[string]any {
	return map[string]any{
		"task_id":   "orchestrion-aspect-valkey-go",
		"prompt_id": "default",
		"prompt":    "make it work",
	}
}

func validSpec() *TaskSpec {
	return &TaskSpec{
		TaskID: "orchestrion-aspect-valkey-go", Prompt: "make it work",
		Mutation:             Mutation{Kind: MutationDeletePaths, Paths: []string{"contrib/valkey-io/valkey-go/orchestrion.yml"}},
		ValidationCommands:   []ValidationCommand{{Label: "build", Command: "go build ./..."}},
		ExpectedChangedPaths: []string{"contrib/valkey-io/valkey-go/"},
		ForbiddenPaths:       []string{"go.sum"}, MaxDiffLines: 200,
		DocsExpectedRead: []string{"contrib/ORCHESTRION.md"},
		OrchestrionYAML:  "contrib/valkey-io/valkey-go/orchestrion.yml",
		UpstreamMarkers:  []string{"pkg.go.dev"},
	}
}

func TestDecodeTaskInputFromPulledRecord(t *testing.T) {
	input, err := DecodeTaskInput(dataset.Record{Input: validInput()})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if input.TaskID != "orchestrion-aspect-valkey-go" || input.PromptID != "default" {
		t.Errorf("input = %+v", input)
	}
}

func TestDecodeTaskInputRejectsUnknownField(t *testing.T) {
	// A typo in a record would otherwise silently disable a criterion, and the run
	// would look valid while measuring less than intended.
	in := validInput()
	in["side"] = "baseline"
	_, err := DecodeTaskInput(dataset.Record{Input: in})
	if err == nil {
		t.Fatal("expected an error for an unknown field")
	}
	if !strings.Contains(err.Error(), "side") {
		t.Errorf("error should name the offending field, got: %v", err)
	}
}

func TestTaskSpecValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*TaskSpec)
		wantErr string
	}{
		{
			name:    "missing task id",
			mutate:  func(s *TaskSpec) { s.TaskID = "" },
			wantErr: "task_id is required",
		},
		{
			name:    "missing prompt",
			mutate:  func(s *TaskSpec) { s.Prompt = "" },
			wantErr: "prompt is required",
		},
		{
			name:    "unknown mutation kind",
			mutate:  func(s *TaskSpec) { s.Mutation.Kind = "rewrite_history" },
			wantErr: "unknown mutation kind",
		},
		{
			name:    "delete_paths without paths",
			mutate:  func(s *TaskSpec) { s.Mutation.Paths = nil },
			wantErr: "requires paths",
		},
		{
			name: "delete_paths with a patch",
			mutate: func(s *TaskSpec) {
				s.Mutation.Patch = "x.patch"
			},
			wantErr: "must not set patch",
		},
		{
			name: "apply_patch without a patch",
			mutate: func(s *TaskSpec) {
				s.Mutation = Mutation{Kind: MutationApplyPatch}
			},
			wantErr: "requires patch",
		},
		{
			name: "apply_patch with paths",
			mutate: func(s *TaskSpec) {
				s.Mutation = Mutation{Kind: MutationApplyPatch, Patch: "x.patch", Paths: []string{"a"}}
			},
			wantErr: "must not set paths",
		},
		{
			name: "apply_patch allowing missing",
			mutate: func(s *TaskSpec) {
				s.Mutation = Mutation{Kind: MutationApplyPatch, Patch: "x.patch", AllowMissing: true}
			},
			wantErr: "allow_missing only applies",
		},
		{
			name: "validation command missing label",
			mutate: func(s *TaskSpec) {
				s.ValidationCommands = []ValidationCommand{{Command: "go build ./..."}}
			},
			wantErr: "needs both label and command",
		},
		{
			name: "duplicate validation label",
			mutate: func(s *TaskSpec) {
				s.ValidationCommands = []ValidationCommand{
					{Label: "build", Command: "go build ./..."},
					{Label: "build", Command: "go vet ./..."},
				}
			},
			wantErr: "duplicate validation label",
		},
		{
			name: "none without assert_absent",
			mutate: func(s *TaskSpec) {
				s.Mutation = Mutation{Kind: MutationNone}
			},
			wantErr: "requires assert_absent",
		},
		{
			name: "none with paths",
			mutate: func(s *TaskSpec) {
				s.Mutation = Mutation{Kind: MutationNone, AssertAbsent: []string{"contrib/x"}, Paths: []string{"a"}}
			},
			wantErr: "must not set paths, patch or allow_missing",
		},
		{
			name: "none allowing missing",
			mutate: func(s *TaskSpec) {
				s.Mutation = Mutation{Kind: MutationNone, AssertAbsent: []string{"contrib/x"}, AllowMissing: true}
			},
			wantErr: "must not set paths, patch or allow_missing",
		},
		{
			name: "assert_absent on a mutating kind",
			mutate: func(s *TaskSpec) {
				s.Mutation.AssertAbsent = []string{"contrib/x"}
			},
			wantErr: "assert_absent only applies",
		},
		{
			name: "source check missing a pattern",
			mutate: func(s *TaskSpec) {
				s.SourceChecks = []SourceCheck{{Label: "option_fn", Paths: []string{"contrib/x/*.go"}}}
			},
			wantErr: "needs label, pattern and paths",
		},
		{
			name: "duplicate source check label",
			mutate: func(s *TaskSpec) {
				s.SourceChecks = []SourceCheck{
					{Label: "option_fn", Paths: []string{"a/*.go"}, Pattern: "x"},
					{Label: "option_fn", Paths: []string{"b/*.go"}, Pattern: "y"},
				}
			},
			wantErr: "duplicate source check label",
		},
		{
			// A bad pattern must fail at seed time, not after the agent has
			// already spent an hour on the task.
			name: "source check with an uncompilable pattern",
			mutate: func(s *TaskSpec) {
				s.SourceChecks = []SourceCheck{{Label: "bad", Paths: []string{"a/*.go"}, Pattern: "([unclosed"}}
			},
			wantErr: "source check \"bad\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := validSpec()
			tt.mutate(spec)
			err := spec.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestCmdResultPassed(t *testing.T) {
	tests := []struct {
		res  CmdResult
		want bool
	}{
		{CmdResult{ExitCode: 0}, true},
		{CmdResult{ExitCode: 1}, false},
		{CmdResult{ExitCode: 0, TimedOut: true}, false},
	}
	for _, tt := range tests {
		if got := tt.res.Passed(); got != tt.want {
			t.Errorf("Passed() = %v for %+v, want %v", got, tt.res, tt.want)
		}
	}
}

func TestOutputResultLookup(t *testing.T) {
	out := &AgentRunOutput{CommandResults: []CmdResult{
		{Label: "build", ExitCode: 0},
		{Label: "vet", ExitCode: 2},
	}}
	if r, ok := out.Result("vet"); !ok || r.ExitCode != 2 {
		t.Errorf("Result(vet) = %+v, %v", r, ok)
	}
	if _, ok := out.Result("nope"); ok {
		t.Error("Result(nope) should not be found")
	}
}

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package agenteval

import "testing"

func TestExperimentSuffix(t *testing.T) {
	tests := map[string]string{
		"origin/main":                     "main",
		"refs/heads/feature/docs":         "feature-docs",
		"rarguelloF/IDMPL-633/agent-eval": "rarguelloF-IDMPL-633-agent-eval",
	}
	for input, want := range tests {
		if got := experimentSuffix(input); got != want {
			t.Errorf("experimentSuffix(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestTaskExperimentName(t *testing.T) {
	spec := &TaskSpec{TaskID: "author-cloudevents-v2", ExperimentName: "author-integration-cloudevents-v2"}
	if got, want := taskExperimentName(spec, "codex", SideBaseline, "main", "pr5052"), "author-integration-cloudevents-v2-codex-main"; got != want {
		t.Errorf("baseline name = %q, want %q", got, want)
	}
	if got, want := taskExperimentName(spec, "codex", SideCandidate, "rarguelloF/IDMPL-611/integration-authoring-docs", "pr5052"), "author-integration-cloudevents-v2-codex-pr5052"; got != want {
		t.Errorf("candidate name = %q, want %q", got, want)
	}
}

func TestTaskExperimentDescription(t *testing.T) {
	spec := &TaskSpec{TaskID: "author-cloudevents-v2"}
	got := taskExperimentDescription(spec, "claude", "claude-sonnet-5", "medium", "pr5052")
	want := "Task: author-cloudevents-v2. Branch: pr5052. Agent: claude. Model: claude-sonnet-5. Effort: medium."
	if got != want {
		t.Errorf("description = %q, want %q", got, want)
	}
}

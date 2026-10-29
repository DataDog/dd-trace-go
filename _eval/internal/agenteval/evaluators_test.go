// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package agenteval

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/llmobs/dataset"
)

func TestScoreEvaluatorRejectsWrongOutputType(t *testing.T) {
	_, err := ScoreEvaluator("score", func(*AgentRunOutput) float64 { return 0 }).Run(
		context.Background(), dataset.Record{}, "not an output")
	if err == nil {
		t.Fatal("expected an error for a non-AgentRunOutput value")
	}
}

func TestEvaluatorsIgnoreMissingTaskOutput(t *testing.T) {
	ctx := context.Background()
	evaluator := ScoreEvaluator("duration", func(*AgentRunOutput) float64 { return 0 })
	got, err := evaluator.Run(ctx, dataset.Record{}, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got != nil {
		t.Errorf("evaluation = %v, want nil", got)
	}
}

func TestTally(t *testing.T) {
	values := map[string]bool{
		CheckAgentExitedOK:           true,
		CheckDiffNotEmpty:            true,
		CheckForbiddenPathsUntouched: false,
	}
	passed, total, score := tally(values)
	if passed != 2 || total != 3 || score != float64(2)/3 {
		t.Errorf("tally = %d/%d (%v), want 2/3", passed, total, score)
	}
}

func TestWeightedTally(t *testing.T) {
	values := map[string]float64{"critical": 1, "partial": 0.5, "failed": 0}
	weights := map[string]float64{"critical": 3, "partial": 2, "failed": 1}
	passed, total, score := weightedTally(values, weights)
	if passed != 1 || total != 3 || score != float64(4)/6 {
		t.Errorf("weightedTally = %d/%d (%v), want 1/3 (2/3 weighted score)", passed, total, score)
	}
}

func TestPathScores(t *testing.T) {
	if got := expectedPathsScore([]string{"a", "b"}, []string{"a", "b", "c", "d"}); got != 0.5 {
		t.Errorf("expected paths score = %v, want 0.5", got)
	}
	if got := forbiddenPathsScore([]string{"ok", "go.mod", "go.sum"}, []string{"go.mod", "go.sum"}); got != float64(1)/3 {
		t.Errorf("forbidden paths score = %v, want 1/3", got)
	}
}

func TestEvaluatorsIgnoreInfrastructureFailure(t *testing.T) {
	evaluator := ScoreEvaluator("score", func(*AgentRunOutput) float64 { return 1 })
	got, err := evaluator.Run(context.Background(), dataset.Record{}, &AgentRunOutput{Status: RunStatusInfrastructureFailure})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got != nil {
		t.Errorf("evaluation = %v, want nil", got)
	}
}

func TestEvaluatorsOnlyIncludeNumericAggregates(t *testing.T) {
	spec := &TaskSpec{
		ValidationCommands:   []ValidationCommand{{Label: "tests", Command: "go test ./..."}},
		ExpectedChangedPaths: []string{"contrib/example/"},
	}
	got := make(map[string]bool)
	for _, evaluator := range Evaluators(spec) {
		got[evaluator.Name()] = true
	}
	for _, want := range []string{
		"checks_score", "validation_score",
		"diff_line_count", "duration_seconds", "docs_read_count", "tool_calls",
	} {
		if !got[want] {
			t.Errorf("missing evaluator %q: %v", want, got)
		}
	}
	for _, unwanted := range []string{
		"check_agent_exited_ok", "check_expected_paths_touched", "validation_tests", "branch",
		"checks_total", "checks_passed", "validation_total", "validation_passed",
		"estimated_cost", "token_count",
	} {
		if got[unwanted] {
			t.Errorf("non-numeric evaluator %q should not be present", unwanted)
		}
	}
}

func TestOrchestrionAspectPresent(t *testing.T) {
	ws := t.TempDir()
	writeFiles(t, ws, map[string]string{
		"good/orchestrion.yml":  "meta:\n  name: thing\naspects: []\n",
		"empty/orchestrion.yml": "   \n",
		"bad/orchestrion.yml":   "meta:\n\tname: [unclosed\n",
	})

	if !orchestrionAspectPresent(ws, "good/orchestrion.yml") {
		t.Error("valid YAML should pass")
	}
	if orchestrionAspectPresent(ws, "missing/orchestrion.yml") {
		t.Error("absent file should fail")
	}
	if orchestrionAspectPresent(ws, "empty/orchestrion.yml") {
		t.Error("empty file should fail")
	}
	// A malformed aspect file is the interesting failure: the build still succeeds
	// and auto-instrumentation silently does nothing.
	if orchestrionAspectPresent(ws, "bad/orchestrion.yml") {
		t.Error("malformed YAML should fail")
	}
}

func TestRegisteredInPackagesGo(t *testing.T) {
	ws := t.TempDir()
	writeFiles(t, ws, map[string]string{
		packagesGoPath: "package instrumentation\n\n// github.com/twmb/franz-go\n",
	})

	if !registeredInPackagesGo(ws, []string{packagesGoPath}, "github.com/twmb/franz-go") {
		t.Error("want true when the file changed and mentions the package")
	}
	// A mention that was already there proves nothing if the agent did not touch it.
	if registeredInPackagesGo(ws, []string{"contrib/x/a.go"}, "github.com/twmb/franz-go") {
		t.Error("want false when the registration file was not changed")
	}
	// An unrelated edit to packages.go is not a registration.
	if registeredInPackagesGo(ws, []string{packagesGoPath}, "github.com/valkey-io/valkey-go") {
		t.Error("want false when the file does not mention the package")
	}
	if err := os.Remove(filepath.Join(ws, packagesGoPath)); err != nil {
		t.Fatal(err)
	}
	if registeredInPackagesGo(ws, []string{packagesGoPath}, "github.com/twmb/franz-go") {
		t.Error("want false when the file is missing")
	}
}

func TestDocsRead(t *testing.T) {
	spec := &TaskSpec{DocsExpectedRead: []string{"contrib/ORCHESTRION.md", "contrib/INTEGRATIONS.md"}}
	run := &RunResult{ToolCalls: []ToolCall{
		{Name: "Read", Input: map[string]any{"file_path": "/ws/contrib/ORCHESTRION.md"}},
		{Name: "Bash", Input: map[string]any{"command": "ls contrib"}},
	}}

	got := docsRead(spec, run, "/ws")
	if len(got) != 1 || got[0] != "contrib/ORCHESTRION.md" {
		t.Errorf("docsRead = %v, want just the doc that was opened", got)
	}

	// Grepping a doc counts as consulting it just as much as Read does.
	run.ToolCalls = append(run.ToolCalls, ToolCall{
		Name:  "Bash",
		Input: map[string]any{"command": "grep -n aspect contrib/INTEGRATIONS.md"},
	})
	if got := docsRead(spec, run, "/ws"); len(got) != 2 {
		t.Errorf("docsRead = %v, want both docs", got)
	}
}

func TestUpstreamConsulted(t *testing.T) {
	spec := &TaskSpec{UpstreamMarkers: []string{"github.com/DataDog/dd-trace-go", "pkg.go.dev/github.com/DataDog/dd-trace-go"}}

	clean := &RunResult{ToolCalls: []ToolCall{
		{Name: "Bash", Input: map[string]any{"command": "go build ./..."}},
	}}
	if upstreamConsulted(spec, clean) {
		t.Error("want false for a run that did not look upstream")
	}

	// The workspace has no history to read the answer from, but the network is
	// still reachable because the agent needs the model API, so this is detection.
	fetched := &RunResult{ToolCalls: []ToolCall{
		{Name: "WebFetch", Input: map[string]any{"url": "https://pkg.go.dev/github.com/DataDog/dd-trace-go/v2"}},
	}}
	if !upstreamConsulted(spec, fetched) {
		t.Error("want true when the agent fetched the reference implementation")
	}

	// Shell commands are deliberately not examined. A marker specific enough to
	// name the upstream source also appears in ordinary commands, so including
	// shell here flagged every run as contaminated. Detection is narrower now,
	// and a shell clone goes unnoticed; that is the accepted trade.
	cloned := &RunResult{ToolCalls: []ToolCall{
		{Name: "Bash", Input: map[string]any{"command": "git clone https://github.com/DataDog/dd-trace-go /tmp/x"}},
	}}
	if upstreamConsulted(spec, cloned) {
		t.Error("shell commands must not count: markers overlap with legitimate command text")
	}

	// The regression that made this criterion useless: the package under
	// instrumentation is a dependency the agent has to reference.
	legitimate := &RunResult{ToolCalls: []ToolCall{
		{Name: "Bash", Input: map[string]any{"command": "cd contrib/valkey-io/valkey-go && go test ./..."}},
		{Name: "Read", Input: map[string]any{"file_path": "contrib/valkey-io/valkey-go/valkey.go"}},
	}}
	if upstreamConsulted(spec, legitimate) {
		t.Error("ordinary work on the target package must not read as contamination")
	}
}

func TestTailAndHeadString(t *testing.T) {
	if got := tailString("abcdef", 100); got != "abcdef" {
		t.Errorf("tailString short = %q", got)
	}
	if got := tailString("abcdef", 3); got[len(got)-3:] != "def" {
		t.Errorf("tailString should keep the end, got %q", got)
	}
	if got := headString("abcdef", 3); got[:3] != "abc" {
		t.Errorf("headString should keep the start, got %q", got)
	}
}

// A task that fails returns a nil *AgentRunOutput, which arrives at the
// evaluator as a non-nil any holding a nil pointer. Dereferencing it panics in
// the evaluator's own goroutine and takes the whole run down, so it has to be
// rejected as an error instead.
func TestEvaluatorRejectsTypedNilOutput(t *testing.T) {
	var out *AgentRunOutput
	var boxed any = out
	if boxed == nil {
		t.Fatal("test is not exercising the typed-nil case")
	}
	if _, err := AsOutput(boxed); err == nil {
		t.Error("AsOutput should reject a nil *AgentRunOutput")
	}

	ev := ScoreEvaluator("score", func(*AgentRunOutput) float64 { return 0 })
	if _, err := ev.Run(context.Background(), dataset.Record{}, boxed); err == nil {
		t.Error("evaluator should return an error rather than panic")
	}
}

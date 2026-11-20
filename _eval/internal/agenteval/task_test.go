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
	"time"
)

// stubRunner stands in for a real agent CLI so the orchestration can be tested
// without spending a model call. act mutates the workspace the way an agent would.
type stubRunner struct {
	act       func(t *testing.T, workspace string) error
	toolCalls []ToolCall
	exitCode  int
	isError   bool
	results   []RunResult
	calls     int
}

func (s *stubRunner) Name() string                   { return "stub" }
func (s *stubRunner) Model() string                  { return "stub-model" }
func (s *stubRunner) Effort() string                 { return "medium" }
func (s *stubRunner) Version(context.Context) string { return "stub-1.0" }

func (s *stubRunner) run(t *testing.T) Runner {
	return &boundStub{s: s, t: t}
}

type boundStub struct {
	s *stubRunner
	t *testing.T
}

func (b *boundStub) Name() string                   { return b.s.Name() }
func (b *boundStub) Model() string                  { return b.s.Model() }
func (b *boundStub) Effort() string                 { return b.s.Effort() }
func (b *boundStub) Version(context.Context) string { return b.s.Version(context.Background()) }

func (b *boundStub) Run(_ context.Context, workspace, _, artifactDir string) (*RunResult, error) {
	b.s.calls++
	if b.s.act != nil {
		if err := b.s.act(b.t, workspace); err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return nil, err
	}
	if len(b.s.results) > 0 {
		index := b.s.calls - 1
		if index >= len(b.s.results) {
			index = len(b.s.results) - 1
		}
		result := b.s.results[index]
		return &result, nil
	}
	return &RunResult{
		FinalMessage: "stub done",
		ExitCode:     b.s.exitCode,
		IsError:      b.s.isError,
		Turns:        3,
		CostUSD:      0.5,
		ToolCalls:    b.s.toolCalls,
	}, nil
}

func TestTaskRunnerRetriesInfrastructureFailure(t *testing.T) {
	repo := fixtureRepo(t)
	stub := &stubRunner{
		act: func(t *testing.T, ws string) error {
			return os.WriteFile(filepath.Join(ws, "contrib/thing/orchestrion.yml"), []byte("meta:\n  name: thing\naspects: []\n"), 0o644)
		},
		results: []RunResult{
			{ExitCode: 137, IsError: true},
			{ExitCode: 0, Turns: 1},
		},
	}
	out, err := newTaskRunner(t, repo, stub).Run(context.Background(), aspectSpec(), "HEAD", SideCandidate)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if stub.calls != 2 {
		t.Fatalf("calls = %d, want 2", stub.calls)
	}
	if out.Status != RunStatusCompleted {
		t.Errorf("status = %q, want %q", out.Status, RunStatusCompleted)
	}
}

func TestTaskRunnerExcludesRepeatedInfrastructureFailure(t *testing.T) {
	repo := fixtureRepo(t)
	stub := &stubRunner{results: []RunResult{{ExitCode: 137, IsError: true}}}
	out, err := newTaskRunner(t, repo, stub).Run(context.Background(), aspectSpec(), "HEAD", SideCandidate)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if stub.calls != 2 {
		t.Fatalf("calls = %d, want 2", stub.calls)
	}
	if out.Status != RunStatusInfrastructureFailure || out.InfrastructureError == "" {
		t.Errorf("unexpected infrastructure result: %+v", out)
	}
}

// fixtureRepo mimics the shape the real suite depends on: a contrib package with an
// Orchestrion aspect file, plus the registration file.
func fixtureRepo(t *testing.T) string {
	return newRepo(t, map[string]string{
		"contrib/thing/thing.go":        "package thing\n",
		"contrib/thing/orchestrion.yml": "meta:\n  name: thing\n",
		"contrib/ORCHESTRION.md":        "# How to write an aspect\n",
		packagesGoPath:                  "package instrumentation\n",
		"go.sum":                        "\n",
	})
}

func aspectSpec() *TaskSpec {
	return &TaskSpec{
		TaskID: "restore-aspect",
		Prompt: "restore the aspect",
		Mutation: Mutation{
			Kind:  MutationDeletePaths,
			Paths: []string{"contrib/thing/orchestrion.yml"},
		},
		ValidationCommands: []ValidationCommand{
			{Label: "build", Command: "true"},
			{Label: "vet", Command: "false"},
		},
		ExpectedChangedPaths: []string{"contrib/thing/orchestrion.yml"},
		ForbiddenPaths:       []string{"go.sum"},
		MaxDiffLines:         50,
		DocsExpectedRead:     []string{"contrib/ORCHESTRION.md"},
		OrchestrionYAML:      "contrib/thing/orchestrion.yml",
		UpstreamMarkers:      []string{"pkg.go.dev"},
	}
}

func newTaskRunner(t *testing.T, repo string, stub *stubRunner) *TaskRunner {
	return &TaskRunner{
		RepoDir:           repo,
		MutationsDir:      t.TempDir(),
		ResultsDir:        t.TempDir(),
		Runner:            stub.run(t),
		ValidationTimeout: 30 * time.Second,
	}
}

func TestTaskRunnerSuccessfulRun(t *testing.T) {
	ctx := context.Background()
	repo := fixtureRepo(t)

	stub := &stubRunner{
		// Restore the deleted aspect file, which is what a successful agent does.
		act: func(t *testing.T, ws string) error {
			return os.WriteFile(filepath.Join(ws, "contrib/thing/orchestrion.yml"),
				[]byte("meta:\n  name: thing\naspects: []\n"), 0o644)
		},
		toolCalls: []ToolCall{
			{Name: "Read", Input: map[string]any{"file_path": "contrib/ORCHESTRION.md"}},
		},
	}
	runner := newTaskRunner(t, repo, stub)

	out, err := runner.Run(ctx, aspectSpec(), "HEAD", SideCandidate)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// The mutation is folded into the base commit, so the diff must describe only
	// the agent's work and not the deletion that set the task up.
	if !containsStr(out.ChangedFiles, "contrib/thing/orchestrion.yml") {
		t.Errorf("ChangedFiles = %v", out.ChangedFiles)
	}
	if len(out.ChangedFiles) != 1 {
		t.Errorf("ChangedFiles = %v, want only the agent's change", out.ChangedFiles)
	}

	want := map[string]float64{
		CheckDiffNotEmpty:            1,
		CheckOrchestrionAspect:       1,
		CheckOrchestrionSchemaValid:  0,
		CheckExpectedPathsTouched:    1,
		CheckForbiddenPathsUntouched: 1,
		CheckDiffWithinLimit:         1,
	}
	if !out.Validations["build"] || out.Validations["vet"] {
		t.Errorf("Validations = %v, want build true and vet false", out.Validations)
	}
	if out.ChecksTotal != 6 || out.ChecksPassed != 5 || out.ChecksScore != float64(5)/6 {
		t.Errorf("check summary = %d/%d (%v), want 5/6", out.ChecksPassed, out.ChecksTotal, out.ChecksScore)
	}
	for name, wantVal := range map[string]bool{
		CheckAgentExitedOK:       true,
		CheckNoPermissionDenials: true,
		CheckDocsOpened:          true,
		CheckNoUpstreamFetch:     true,
	} {
		if got := out.Diagnostics[name]; got != wantVal {
			t.Errorf("diagnostic %q = %v, want %v", name, got, wantVal)
		}
	}
	if out.ValidationTotal != 2 || out.ValidationPassed != 1 || out.ValidationScore != 0.5 {
		t.Errorf("validation summary = %d/%d (%v), want 1/2", out.ValidationPassed, out.ValidationTotal, out.ValidationScore)
	}
	for name, wantVal := range want {
		got, ok := out.Checks[name]
		if !ok {
			t.Errorf("check %q missing", name)
			continue
		}
		if got != wantVal {
			t.Errorf("check %q = %v, want %v", name, got, wantVal)
		}
	}
	// registered_in_packages_go was not declared, so it must not be scored at all.
	if _, ok := out.Checks[CheckRegisteredInPackagesGo]; ok {
		t.Error("undeclared criterion should be absent, not false")
	}

	if out.CostUSD != 0.5 || out.Turns != 3 {
		t.Errorf("run metadata not carried through: %+v", out)
	}
	for _, name := range []string{"output.json", "changes.diff"} {
		if _, err := os.Stat(filepath.Join(out.ArtifactDir, name)); err != nil {
			t.Errorf("artifact %s missing: %v", name, err)
		}
	}
	// Workspaces are a full repo copy, so they must be cleaned up by default.
	if _, err := os.Stat(filepath.Join(out.ArtifactDir, "workspace")); !os.IsNotExist(err) {
		t.Errorf("workspace should be removed, err = %v", err)
	}
}

func TestTaskRunnerAgentDidNothing(t *testing.T) {
	ctx := context.Background()
	repo := fixtureRepo(t)
	runner := newTaskRunner(t, repo, &stubRunner{})

	out, err := runner.Run(ctx, aspectSpec(), "HEAD", SideBaseline)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// An agent that changed nothing is a datapoint, not a harness error.
	if out.Checks[CheckDiffNotEmpty] != 0 {
		t.Error("diff_not_empty should be false")
	}
	if out.Checks[CheckOrchestrionAspect] != 0 {
		t.Error("orchestrion_aspect_present should be false: the file is still deleted")
	}
	if out.Checks[CheckExpectedPathsTouched] != 0 {
		t.Error("expected_paths_touched should be false")
	}
	if out.Diagnostics[CheckDocsOpened] {
		t.Error("docs_opened should be false when no tool calls happened")
	}
	// Not touching a forbidden path is still a pass, even for a run that did nothing.
	if out.Checks[CheckForbiddenPathsUntouched] != 1 {
		t.Error("forbidden_paths_untouched should be true")
	}
}

func TestTaskRunnerDetectsForbiddenPathAndContamination(t *testing.T) {
	ctx := context.Background()
	repo := fixtureRepo(t)

	stub := &stubRunner{
		act: func(t *testing.T, ws string) error {
			return os.WriteFile(filepath.Join(ws, "go.sum"), []byte("tampered\n"), 0o644)
		},
		toolCalls: []ToolCall{
			{Name: "WebFetch", Input: map[string]any{"url": "https://pkg.go.dev/x"}},
		},
	}
	runner := newTaskRunner(t, repo, stub)

	out, err := runner.Run(ctx, aspectSpec(), "HEAD", SideCandidate)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out.Checks[CheckForbiddenPathsUntouched] != 0.5 {
		t.Errorf("forbidden_paths = %v, want 0.5", out.Checks[CheckForbiddenPathsUntouched])
	}
	if out.Diagnostics[CheckNoUpstreamFetch] {
		t.Error("no_upstream_fetch should be false after fetching pkg.go.dev")
	}
}

func TestTaskRunnerFailsOnStaleMutation(t *testing.T) {
	ctx := context.Background()
	// No aspect file, so the record's mutation cannot apply.
	repo := newRepo(t, map[string]string{"contrib/thing/thing.go": "package thing\n"})
	runner := newTaskRunner(t, repo, &stubRunner{})

	if _, err := runner.Run(ctx, aspectSpec(), "HEAD", SideBaseline); err == nil {
		t.Fatal("expected an error rather than a result that looks valid but measures nothing")
	}
}

func TestTaskRunnerKeepWorkspaces(t *testing.T) {
	ctx := context.Background()
	repo := fixtureRepo(t)
	runner := newTaskRunner(t, repo, &stubRunner{})
	runner.KeepWorkspaces = true

	out, err := runner.Run(ctx, aspectSpec(), "HEAD", SideBaseline)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out.ArtifactDir, "workspace", "contrib/thing/thing.go")); err != nil {
		t.Errorf("workspace should be kept: %v", err)
	}
}

func TestRunValidationCapturesFailureAndTimeout(t *testing.T) {
	ctx := context.Background()
	ws := t.TempDir()

	results := RunValidation(ctx, ws, []ValidationCommand{
		{Label: "ok", Command: "echo hello"},
		{Label: "fails", Command: "echo boom >&2; exit 3"},
		{Label: "slow", Command: "sleep 5"},
	}, 300*time.Millisecond)

	if len(results) != 3 {
		t.Fatalf("got %d results", len(results))
	}
	if !results[0].Passed() || results[0].Stdout == "" {
		t.Errorf("ok = %+v", results[0])
	}
	if results[1].ExitCode != 3 || results[1].Passed() {
		t.Errorf("fails = %+v", results[1])
	}
	if results[1].Stderr == "" {
		t.Errorf("stderr should be captured for diagnosis: %+v", results[1])
	}
	// A killed command is distinguished from one that genuinely failed.
	if !results[2].TimedOut || results[2].Passed() {
		t.Errorf("slow = %+v", results[2])
	}
}

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package suites

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/_eval/internal/agenteval"
)

func TestRegistryIsNotEmpty(t *testing.T) {
	if len(All()) == 0 {
		t.Fatal("no suites are registered")
	}
	for _, name := range Names() {
		if _, err := Lookup(name); err != nil {
			t.Errorf("Names lists %q but Lookup rejects it: %v", name, err)
		}
	}
}

func TestLookupUnknownSuiteListsAlternatives(t *testing.T) {
	_, err := Lookup("no-such-suite")
	if err == nil {
		t.Fatal("expected an error for an unknown suite")
	}
	// The error is the discovery path when someone guesses a name, so it has to
	// carry the real ones.
	for _, name := range Names() {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error should list %q, got: %v", name, err)
		}
	}
}

func TestSuitesAreValid(t *testing.T) {
	datasets := map[string]string{}
	for _, s := range All() {
		t.Run(s.Name, func(t *testing.T) {
			if err := Validate(s); err != nil {
				t.Fatalf("invalid suite: %v", err)
			}
			// Two suites sharing a dataset prefix would create colliding task
			// dataset names.
			if other, dup := datasets[s.Dataset]; dup {
				t.Errorf("dataset %q is already used by suite %q", s.Dataset, other)
			}
			datasets[s.Dataset] = s.Name

			records, err := s.Records()
			if err != nil {
				t.Fatalf("records: %v", err)
			}
			if len(records) < len(s.Tasks) {
				t.Errorf("got %d records for %d tasks, want at least one prompt per task", len(records), len(s.Tasks))
			}
			for i, rec := range records {
				meta, ok := rec.Metadata.(map[string]any)
				if !ok {
					t.Fatalf("record %d: metadata is %T", i, rec.Metadata)
				}
				if meta["suite"] != s.Name {
					t.Errorf("record %d: suite = %v, want %q", i, meta["suite"], s.Name)
				}
			}
		})
	}
}

func TestTaskDatasetNames(t *testing.T) {
	suite, err := Lookup("integration-authoring")
	if err != nil {
		t.Fatal(err)
	}
	got := suite.DatasetName("author-cloudevents-v2")
	want := "dd-trace-go-integration-authoring-author-cloudevents-v2"
	if got != want {
		t.Errorf("dataset name = %q, want %q", got, want)
	}
}

func TestDatasetRecordsContainOnlyPromptInput(t *testing.T) {
	for _, s := range All() {
		records, err := s.Records()
		if err != nil {
			t.Fatalf("%s: %v", s.Name, err)
		}
		for i, rec := range records {
			input, err := agenteval.DecodeTaskInput(rec)
			if err != nil {
				t.Fatalf("%s record %d: %v", s.Name, i, err)
			}
			if input.TaskID == "" || input.PromptID == "" || input.Prompt == "" {
				t.Errorf("%s record %d: incomplete input %+v", s.Name, i, input)
			}
		}
	}
}

func TestCloudEventsHasThreePromptVariants(t *testing.T) {
	suite, err := Lookup("integration-authoring")
	if err != nil {
		t.Fatal(err)
	}
	records, err := suite.Records()
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{}
	for _, rec := range records {
		input, err := agenteval.DecodeTaskInput(rec)
		if err != nil {
			t.Fatal(err)
		}
		if input.TaskID == "author-cloudevents-v2" {
			cases[input.PromptID] = true
		}
	}
	for _, want := range []string{
		"add-support",
		"new-integration",
		"follow-repo-guidance",
	} {
		if !cases[want] {
			t.Errorf("missing CloudEvents case %q", want)
		}
	}
	if len(cases) != 3 {
		t.Errorf("CloudEvents cases = %v, want 3", cases)
	}
}

func TestCloudEventsUsesRepositoryChecks(t *testing.T) {
	suite, err := Lookup("integration-authoring")
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range suite.Specs() {
		if spec.TaskID != "author-cloudevents-v2" {
			continue
		}
		got := make(map[string]string, len(spec.ValidationCommands))
		for _, command := range spec.ValidationCommands {
			got[command.Label] = command.Command
		}
		for label, target := range map[string]string{
			"fix_modules": "make fix-modules",
			"lint":        "make lint",
			"tests":       "make test",
		} {
			if !strings.Contains(got[label], target) {
				t.Errorf("%s command = %q, want %q", label, got[label], target)
			}
		}
		return
	}
	t.Fatal("author-cloudevents-v2 task not found")
}

func TestCloudEventsExpectedAndForbiddenPaths(t *testing.T) {
	suite, err := Lookup("integration-authoring")
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range suite.Specs() {
		if spec.TaskID != "author-cloudevents-v2" {
			continue
		}
		wantChanged := []string{
			"contrib/cloudevents/sdk-go.v2/cloudevents.go",
			"contrib/cloudevents/sdk-go.v2/cloudevents_test.go",
			"contrib/cloudevents/sdk-go.v2/example_test.go",
			"contrib/cloudevents/sdk-go.v2/go.mod",
			"contrib/cloudevents/sdk-go.v2/go.sum",
			"contrib/cloudevents/sdk-go.v2/option.go",
			"contrib/cloudevents/sdk-go.v2/orchestrion.yml",
			"ddtrace/tracer/option.go",
			"go.work",
			"go.work.sum",
			"instrumentation/packages.go",
			"internal/env/supported_configurations.gen.go",
			"internal/env/supported_configurations.json",
			"internal/orchestrion/_integration/cloudevents-sdk-go.v2/cloudevents.go",
			"internal/orchestrion/_integration/cloudevents-sdk-go.v2/generated_test.go",
			"internal/orchestrion/_integration/go.mod",
			"internal/orchestrion/_integration/go.sum",
			"internal/stacktrace/contribs_generated.go",
			"orchestrion/all/go.mod",
			"orchestrion/all/go.sum",
			"orchestrion/all/orchestrion.tool.go",
		}
		if !slices.Equal(spec.ExpectedChangedPaths, wantChanged) {
			t.Errorf("expected changed paths = %v, want %v", spec.ExpectedChangedPaths, wantChanged)
		}
		for _, path := range []string{"go.mod", "go.sum"} {
			if !slices.Contains(spec.ForbiddenPaths, path) {
				t.Errorf("forbidden paths %v do not contain %q", spec.ForbiddenPaths, path)
			}
		}
		return
	}
	t.Fatal("author-cloudevents-v2 task not found")
}

// TestSuitesAreDocSensitive guards the property the whole eval depends on: for a
// docs-only diff, the two sides can only differ on tasks where consulting the
// docs is what makes the difference. A task with no expected docs and no
// doc-specific criterion scores identically on both sides and measures nothing.
func TestSuitesAreDocSensitive(t *testing.T) {
	for _, s := range All() {
		for _, spec := range s.Specs() {
			t.Run(s.Name+"/"+spec.TaskID, func(t *testing.T) {
				if len(spec.DocsExpectedRead) == 0 {
					t.Error("no docs_expected_read, so it cannot show a docs effect")
				}
				if spec.OrchestrionYAML == "" && spec.RegistrationImport == "" &&
					len(spec.SourceChecks) == 0 && len(spec.RequiredPaths) == 0 {
					t.Error("declares no doc-specific criterion")
				}
				if len(spec.ValidationCommands) == 0 {
					t.Error("no validation commands")
				}
				if len(spec.ForbiddenPaths) == 0 {
					t.Error("no forbidden paths")
				}
				if spec.MaxDiffLines <= 0 {
					t.Error("no diff size limit")
				}
				if len(spec.UpstreamMarkers) == 0 {
					t.Error("no upstream markers, so contamination would go undetected")
				}
			})
		}
	}
}

// TestMutationsApplyToHEAD is the staleness guard. Mutations reference real
// paths, so they rot as main advances. Failing here costs seconds; discovering
// it during a comparison costs hours.
func TestMutationsApplyToHEAD(t *testing.T) {
	if testing.Short() {
		t.Skip("materialises the repo tree")
	}
	ctx := context.Background()
	repoDir, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	head, err := agenteval.ResolveRef(ctx, repoDir, "HEAD")
	if err != nil {
		t.Skipf("not a git checkout: %v", err)
	}

	for _, s := range All() {
		t.Run(s.Name, func(t *testing.T) {
			mutationsDir, err := filepath.Abs(filepath.Join("..", "mutations", s.Name))
			if err != nil {
				t.Fatal(err)
			}
			if err := agenteval.VerifyMutations(ctx, repoDir, mutationsDir, head, s.Specs()); err != nil {
				t.Fatalf("a task mutation no longer applies to HEAD, so the record is stale: %v", err)
			}
		})
	}
}

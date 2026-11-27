// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package agenteval

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sourceTree(t *testing.T) string {
	t.Helper()
	return newRepo(t, map[string]string{
		"contrib/thing/option.go": "package thing\n\ntype Option interface{ apply(*config) }\n\ntype OptionFn func(*config)\n",
		"contrib/thing/thing.go":  "package thing\n\nconst tag = \"thing.event_id\"\n",
		"instrumentation/packages.go": "package instrumentation\n\nvar packages = map[Package]PackageInfo{\n" +
			"\tPackageOther: {TracedPackage: \"other\", naming: map[Component]componentNames{}},\n" +
			"\tPackageThing: {TracedPackage: \"thing\"},\n}\n",
	})
}

func TestSourceCheckPresence(t *testing.T) {
	tree := sourceTree(t)
	goFiles := []string{"contrib/thing/*.go"}

	tests := []struct {
		name  string
		check SourceCheck
		want  bool
	}{
		{
			name:  "present in one of several globbed files",
			check: SourceCheck{Label: "option_fn", Paths: goFiles, Pattern: `(?m)^type OptionFn\b`},
			want:  true,
		},
		{
			name:  "absent when nothing declares it",
			check: SourceCheck{Label: "custom_tag", Paths: goFiles, Pattern: `func WithCustomTag\(`},
			want:  false,
		},
		{
			name:  "inverted check passes when no file matches",
			check: SourceCheck{Label: "no_op_name", Paths: goFiles, Pattern: `instr\.OperationName\(`, Absent: true},
			want:  true,
		},
		{
			name:  "inverted check fails when one file matches",
			check: SourceCheck{Label: "no_option_fn", Paths: goFiles, Pattern: `(?m)^type OptionFn\b`, Absent: true},
			want:  false,
		},
		{
			name:  "exact path rather than a glob",
			check: SourceCheck{Label: "tag", Paths: []string{"contrib/thing/thing.go"}, Pattern: `thing\.event_id`},
			want:  true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sourceCheckSatisfied(tree, tc.check); got != tc.want {
				t.Errorf("sourceCheckSatisfied = %v, want %v", got, tc.want)
			}
		})
	}
}

// A glob selecting nothing must fail in both directions. Otherwise an agent
// could satisfy every Absent criterion by deleting the file it is judged on,
// and a missing integration test package would score as compliant.
func TestSourceCheckFailsWhenGlobSelectsNothing(t *testing.T) {
	tree := sourceTree(t)
	for _, absent := range []bool{false, true} {
		check := SourceCheck{
			Label:   "integration_test",
			Paths:   []string{"internal/orchestrion/_integration/*thing*/*.go"},
			Pattern: `(?s).`,
			Absent:  absent,
		}
		if sourceCheckSatisfied(tree, check) {
			t.Errorf("absent=%v: a check over files that do not exist must not pass", absent)
		}
	}
}

// The naming-map check has to fire on the entry under test without being
// tripped by every other entry in the same file.
func TestSourceCheckWindowedNamingMap(t *testing.T) {
	tree := sourceTree(t)
	check := SourceCheck{
		Label:   "no_legacy_naming_map",
		Paths:   []string{"instrumentation/packages.go"},
		Pattern: `PackageThing[\s\S]{0,400}?(naming:|buildOpName|buildServiceName)`,
		Absent:  true,
	}
	if !sourceCheckSatisfied(tree, check) {
		t.Error("an entry with no naming map should pass even though another entry in the file has one")
	}

	withNaming := strings.Replace(
		readFile(t, filepath.Join(tree, "instrumentation/packages.go")),
		`PackageThing: {TracedPackage: "thing"}`,
		`PackageThing: {TracedPackage: "thing", naming: map[Component]componentNames{}}`,
		1)
	writeFile(t, filepath.Join(tree, "instrumentation/packages.go"), withNaming)

	if sourceCheckSatisfied(tree, check) {
		t.Error("an entry that does carry a naming map should fail")
	}
}

func TestSourceCheckRejectsEscapingGlob(t *testing.T) {
	tree := sourceTree(t)
	check := SourceCheck{Label: "escape", Paths: []string{"../../*.go"}, Pattern: `.`}
	if sourceCheckSatisfied(tree, check) {
		t.Error("a glob escaping the workspace must not be evaluated")
	}
}

func TestAllPathsExist(t *testing.T) {
	tree := sourceTree(t)
	if err := os.MkdirAll(filepath.Join(tree, "internal/orchestrion/_integration/empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		paths []string
		want  bool
	}{
		{"file that exists", []string{"contrib/thing/option.go"}, true},
		{"directory with contents", []string{"contrib/thing"}, true},
		{"missing path", []string{"contrib/thing/missing.go"}, false},
		{"one of several missing", []string{"contrib/thing/option.go", "nope"}, false},
		// A test package that exists but holds no files is the same failure as
		// not having written one.
		{"empty directory", []string{"internal/orchestrion/_integration/empty"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := allPathsExist(tree, tc.paths); got != tc.want {
				t.Errorf("allPathsExist = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMutationNoneChecksAbsence(t *testing.T) {
	ctx := context.Background()
	tree := sourceTree(t)

	ok := Mutation{Kind: MutationNone, AssertAbsent: []string{"contrib/cloudevents"}}
	if err := ApplyMutation(ctx, tree, "", ok); err != nil {
		t.Errorf("absent path should apply cleanly: %v", err)
	}
	if err := CheckMutation(ctx, tree, "", ok); err != nil {
		t.Errorf("CheckMutation should agree: %v", err)
	}

	stale := Mutation{Kind: MutationNone, AssertAbsent: []string{"contrib/thing"}}
	err := ApplyMutation(ctx, tree, "", stale)
	if err == nil {
		t.Fatal("expected an error: once the thing exists the task no longer asks for something missing")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error should say what went stale, got: %v", err)
	}
}

// ApplyMutation for the none kind must not touch the tree.
func TestMutationNoneLeavesTreeAlone(t *testing.T) {
	ctx := context.Background()
	tree := sourceTree(t)
	before := readFile(t, filepath.Join(tree, "contrib/thing/option.go"))

	m := Mutation{Kind: MutationNone, AssertAbsent: []string{"contrib/cloudevents"}}
	if err := ApplyMutation(ctx, tree, "", m); err != nil {
		t.Fatal(err)
	}
	if after := readFile(t, filepath.Join(tree, "contrib/thing/option.go")); after != before {
		t.Error("the none mutation must leave the workspace untouched")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

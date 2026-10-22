// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package orchtool

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// orchestrionFixtureModule is the nested module whose go.mod pins the
// orchestrion version the fixture tests drive.
var orchestrionFixtureModule = filepath.Join("..", "..", "fixtures", "itrbackfill", "orchestrion")

func TestCommandUsesThePathBinary(t *testing.T) {
	executable, args := Command("/ci/orchestrion", "go", "go", "test", "-c", "-o", "app.test")
	if executable != "/ci/orchestrion" {
		t.Fatalf("executable = %q, want %q", executable, "/ci/orchestrion")
	}
	want := []string{"go", "test", "-c", "-o", "app.test"}
	if !slices.Equal(args, want) {
		t.Fatalf("args = %q, want %q", args, want)
	}
}

func TestCommandFallsBackToGoRun(t *testing.T) {
	executable, args := Command("", "go", "go", "test", "-c", "-o", "app.test")
	if executable != "go" {
		t.Fatalf("executable = %q, want %q", executable, "go")
	}
	want := []string{
		"run", "-mod=readonly", "github.com/DataDog/orchestrion",
		"go", "test", "-c", "-o", "app.test",
	}
	if !slices.Equal(args, want) {
		t.Fatalf("args = %q, want %q", args, want)
	}
}

func TestCommandDoesNotModifyInputArgs(t *testing.T) {
	args := []string{"go", "test"}
	_, _ = Command("", "go", args...)
	if !slices.Equal(args, []string{"go", "test"}) {
		t.Fatalf("Command modified its input args: %q", args)
	}
}

func TestParseVersion(t *testing.T) {
	tests := []struct {
		output string
		want   string
	}{
		{output: "orchestrion v1.9.0\n", want: "v1.9.0"},
		{output: "orchestrion v1.13.0-rc.1\n", want: "v1.13.0-rc.1"},
		{output: "orchestrion\n", want: ""},
		{output: "v0.0.0\n", want: "v0.0.0"},
		{output: "", want: ""},
	}
	for _, test := range tests {
		if got := parseVersion([]byte(test.output)); got != test.want {
			t.Errorf("parseVersion(%q) = %q, want %q", test.output, got, test.want)
		}
	}
}

func TestModuleVersionReportsTheFixturePin(t *testing.T) {
	version, err := ModuleVersion("go", orchestrionFixtureModule)
	if err != nil {
		t.Fatalf("ModuleVersion: %v", err)
	}
	if !strings.HasPrefix(version, "v") || len(version) < 2 || version[1] < '0' || version[1] > '9' {
		t.Fatalf("ModuleVersion = %q, want a v-prefixed semantic version", version)
	}
}

func TestModuleVersionFailsOutsideAModule(t *testing.T) {
	if _, err := ModuleVersion("go", t.TempDir()); err == nil {
		t.Fatal("ModuleVersion must fail in a directory without a go.mod")
	}
}

func TestBinaryVersionFailsForAMissingBinary(t *testing.T) {
	if _, err := BinaryVersion("/does/not/exist"); err == nil {
		t.Fatal("BinaryVersion must fail for a missing binary")
	}
}

func TestLocateMatchesThePinnedVersion(t *testing.T) {
	path := Locate("go", orchestrionFixtureModule)
	if path == "" {
		t.Skip("no orchestrion binary on PATH reports the fixture pin")
	}
	got, err := BinaryVersion(path)
	if err != nil {
		t.Fatalf("BinaryVersion(%q): %v", path, err)
	}
	want, err := ModuleVersion("go", orchestrionFixtureModule)
	if err != nil {
		t.Fatalf("ModuleVersion: %v", err)
	}
	if got != want {
		t.Fatalf("Locate returned %q reporting %q, want a binary reporting %q", path, got, want)
	}
}

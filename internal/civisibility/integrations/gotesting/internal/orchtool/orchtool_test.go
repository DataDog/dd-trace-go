// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package orchtool

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
		{output: "orchestrion\r\n", want: ""},
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

// fakeOrchestrion copies the test binary into dir under the file name that
// LookPath resolves for orchestrion. The copy reports the given version
// through TestMain. A copy of the test binary is a native executable on
// every host, so the fake never depends on script execution. Put dir first
// on PATH to make the fake the binary Locate finds.
func fakeOrchestrion(t *testing.T, dir, version string) {
	t.Helper()
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatalf("locate the test binary: %v", err)
	}
	fake, err := os.ReadFile(testBinary)
	if err != nil {
		t.Fatalf("read the test binary %s: %v", testBinary, err)
	}
	if err := os.WriteFile(fakeOrchestrionPath(dir), fake, 0o755); err != nil {
		t.Fatalf("write the fake orchestrion binary: %v", err)
	}
	// BinaryVersion inherits the parent environment, so TestMain reads this
	// variable when it runs the copy.
	t.Setenv(fakeOrchestrionVersionEnv, version)
}

// fakeOrchestrionPath returns the path of the fake binary that LookPath
// resolves on the host running the test.
func fakeOrchestrionPath(dir string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(dir, "orchestrion.exe")
	}
	return filepath.Join(dir, "orchestrion")
}

func prependPath(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestLocateSelectsABinaryThatReportsThePinnedVersion(t *testing.T) {
	pinned, err := ModuleVersion("go", orchestrionFixtureModule)
	if err != nil {
		t.Fatalf("ModuleVersion: %v", err)
	}
	dir := t.TempDir()
	fakeOrchestrion(t, dir, pinned)
	prependPath(t, dir)
	if got := Locate("go", orchestrionFixtureModule); got != fakeOrchestrionPath(dir) {
		t.Fatalf("Locate = %q, want the fake binary at the pinned version %s", got, pinned)
	}
}

func TestLocateRejectsABinaryThatReportsADifferentVersion(t *testing.T) {
	dir := t.TempDir()
	fakeOrchestrion(t, dir, "v0.0.0-mismatch")
	prependPath(t, dir)
	if got := Locate("go", orchestrionFixtureModule); got != "" {
		t.Fatalf("Locate = %q, want no binary: a version mismatch makes orchestrion exit 255", got)
	}
}

// fakeOrchestrionVersionEnv holds the version that the fake orchestrion
// binary reports. TestMain reads it, so a copy of the test binary can stand
// in for the CLI.
const fakeOrchestrionVersionEnv = "DD_TEST_FAKE_ORCHESTRION_VERSION"

// TestMain turns the test binary into the fake orchestrion binary. When
// fakeOrchestrionVersionEnv is set, the binary reports that version and
// exits before the test framework runs, so the copy answers `version` like
// the CLI does.
func TestMain(m *testing.M) {
	if version := os.Getenv(fakeOrchestrionVersionEnv); version != "" {
		fmt.Printf("orchestrion %s\n", version)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

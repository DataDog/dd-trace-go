// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package orchtool

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// orchestrionFixtureModule is the nested module whose go.mod pins the
// orchestrion version the fixture tests drive.
var orchestrionFixtureModule = filepath.Join("..", "..", "fixtures", "itrbackfill", "orchestrion")

// repositoryRoot is the checkout root that holds the canonical orchestrion
// version in .orchestrion-version.
var repositoryRoot = filepath.Join("..", "..", "..", "..", "..", "..")

func TestLocateReturnsTheExportedBinary(t *testing.T) {
	t.Setenv(OrchestrionBinEnv, "/ci/orchestrion")
	if got := Locate(); got != "/ci/orchestrion" {
		t.Fatalf("Locate = %q, want the path exported in %s", got, OrchestrionBinEnv)
	}
}

func TestCommandUsesTheExportedBinary(t *testing.T) {
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

func TestFixturePinMatchesTheCanonicalOrchestrionVersion(t *testing.T) {
	// The fixture module is not part of go.work, so GOWORK=off makes go list
	// report the go.mod pin instead of the workspace's orchestrion version.
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Version}}", "github.com/DataDog/orchestrion")
	cmd.Dir = orchestrionFixtureModule
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("read the orchestrion pin of %s: %v", orchestrionFixtureModule, err)
	}
	versionFile := filepath.Join(repositoryRoot, ".orchestrion-version")
	canonical, err := os.ReadFile(versionFile)
	if err != nil {
		t.Fatalf("read %s: %v", versionFile, err)
	}
	if got, want := strings.TrimSpace(string(out)), strings.TrimSpace(string(canonical)); got != want {
		t.Fatalf("the fixture module %s pins orchestrion %s but .orchestrion-version holds %s; run make upgrade/orchestrion to move the pin with the canonical version", filepath.Join(orchestrionFixtureModule, "go.mod"), got, want)
	}
}

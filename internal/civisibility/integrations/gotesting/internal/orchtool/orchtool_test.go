// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package orchtool

import (
	"os"
	"slices"
	"testing"
)

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

func TestLocate(t *testing.T) {
	path := Locate()
	if path == "" {
		t.Skip("no orchestrion binary on PATH")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Locate returned %q, which is not readable: %v", path, err)
	}
}

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build !windows

package orchtool

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeOrchestrion writes a shell script named orchestrion into dir that
// reports the given version, and returns the directory. Put dir first on PATH
// to make it the binary Locate finds.
func fakeOrchestrion(t *testing.T, dir, version string) {
	t.Helper()
	script := "#!/bin/sh\necho 'orchestrion " + version + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "orchestrion"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake orchestrion: %v", err)
	}
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
	if got := Locate("go", orchestrionFixtureModule); got != filepath.Join(dir, "orchestrion") {
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

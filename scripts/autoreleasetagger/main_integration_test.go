// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// Package main_test contains integration tests for the autoreleasetagger tool.
package main

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// copyTestdata copies src into dst, creating parent directories as needed.
func copyTestdata(t *testing.T, src, dst string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatalf("failed to create dir for %s: %v", dst, err)
	}

	in, err := os.Open(src)
	if err != nil {
		t.Fatalf("failed to open %s: %v", src, err)
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		t.Fatalf("failed to create %s: %v", dst, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		t.Fatalf("failed to copy %s to %s: %v", src, dst, err)
	}
}

// scaffoldRepo copies the testdata tree into tmpDir and initialises a git repo
// on the given branch with a single initial commit. The caller receives the
// absolute path of the repo root (same as tmpDir).
func scaffoldRepo(t *testing.T, branch string) string {
	t.Helper()

	tmpDir := t.TempDir()

	// Copy all testdata files, preserving the directory structure.
	src := "testdata/root"
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(tmpDir, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		copyTestdata(t, path, dst)
		return nil
	})
	if err != nil {
		t.Fatalf("failed to copy testdata: %v", err)
	}

	// Initialise git repo.
	gitSetup := [][]string{
		{"init"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test User"},
		{"checkout", "-b", branch},
		{"add", "."},
		{"commit", "-m", "initial commit"},
	}
	for _, args := range gitSetup {
		if err := runGitCommand(tmpDir, args...); err != nil {
			t.Fatalf("git %s failed: %v", args[0], err)
		}
	}

	return tmpDir
}

// testLogger returns a debug-level slog logger and installs it as the default.
func testLogger() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})))
}

// TestSingleCommitMultiTag is the primary acceptance test: one invocation must
// produce exactly one new commit and all tags pointing at that commit.
func TestSingleCommitMultiTag(t *testing.T) {
	t.Parallel()
	testLogger()

	const (
		branch  = "release-v2.9.x"
		version = "v2.9.9-rc.1"
	)

	tmpDir := scaffoldRepo(t, branch)

	if err := run(false, "test-remote", true, tmpDir, version, []string{}, []string{}); err != nil {
		t.Fatalf("autoreleasetagger failed: %v", err)
	}

	// Exactly one new commit on top of the initial commit.
	commits, err := runGitCommandWithOutput(tmpDir, "log", "--oneline")
	if err != nil {
		t.Fatalf("failed to read commits: %v", err)
	}
	commitLines := nonEmptyLines(commits)
	if len(commitLines) != 2 {
		t.Errorf("expected 2 commits (initial + release), got %d:\n%s", len(commitLines), commits)
	}

	// The release commit message must follow the required format.
	expectedMsg := fmt.Sprintf("internal/version: %s", version)
	if !strings.Contains(commitLines[0], expectedMsg) {
		t.Errorf("release commit message %q does not contain %q", commitLines[0], expectedMsg)
	}

	// Collect tags that point at HEAD.
	tagsAtHead, err := runGitCommandWithOutput(tmpDir, "tag", "--points-at", "HEAD")
	if err != nil {
		t.Fatalf("failed to list tags at HEAD: %v", err)
	}

	// Root tag must be present.
	if !containsLine(tagsAtHead, version) {
		t.Errorf("root tag %q not found in tags at HEAD:\n%s", version, tagsAtHead)
	}

	// Contrib tags must be present and all point at the same (single) commit.
	for _, sub := range []string{"moduleA", "moduleB"} {
		tag := fmt.Sprintf("%s/%s", sub, version)
		if !containsLine(tagsAtHead, tag) {
			t.Errorf("submodule tag %q not found in tags at HEAD:\n%s", tag, tagsAtHead)
		}
	}

	// Confirm version file was updated.
	assertVersionFile(t, tmpDir, version)
}

// TestIdempotency verifies that running autoreleasetagger twice with the same
// version is a no-op: no new commit, no duplicate tags.
func TestIdempotency(t *testing.T) {
	t.Parallel()
	testLogger()

	const (
		branch  = "release-v2.9.x"
		version = "v2.9.9-rc.1"
	)

	tmpDir := scaffoldRepo(t, branch)

	runOnce := func() {
		t.Helper()
		if err := run(false, "test-remote", true, tmpDir, version, []string{}, []string{}); err != nil {
			t.Fatalf("autoreleasetagger failed: %v", err)
		}
	}

	runOnce() // first run
	runOnce() // second run — must be a no-op

	commits, err := runGitCommandWithOutput(tmpDir, "log", "--oneline")
	if err != nil {
		t.Fatalf("failed to read commits: %v", err)
	}
	if got := len(nonEmptyLines(commits)); got != 2 {
		t.Errorf("expected 2 commits after idempotent run, got %d:\n%s", got, commits)
	}

	// No duplicate tags.
	tags, err := runGitCommandWithOutput(tmpDir, "tag", "--list", version)
	if err != nil {
		t.Fatalf("failed to list tags: %v", err)
	}
	if got := len(nonEmptyLines(tags)); got != 1 {
		t.Errorf("expected exactly 1 root tag, got %d", got)
	}
}

// TestVersionBranchMismatch verifies that a version whose major.minor does not
// match the release branch is rejected.
func TestVersionBranchMismatch(t *testing.T) {
	t.Parallel()
	testLogger()

	tmpDir := scaffoldRepo(t, "release-v2.9.x")

	err := run(false, "test-remote", true, tmpDir, "v2.8.0-rc.1", []string{}, []string{})
	if err == nil {
		t.Fatal("expected error for version/branch mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "mismatch") {
		t.Errorf("error should mention mismatch, got: %v", err)
	}
}

// TestInvalidVersion verifies that a malformed version string is rejected.
func TestInvalidVersion(t *testing.T) {
	t.Parallel()
	testLogger()

	tmpDir := scaffoldRepo(t, "release-v2.9.x")

	for _, bad := range []string{"2.9.0", "v2.9", "vX.Y.Z", "v2.9.0-beta.1"} {
		bad := bad
		t.Run(bad, func(t *testing.T) {
			t.Parallel()
			err := run(false, "test-remote", true, tmpDir, bad, []string{}, []string{})
			if err == nil {
				t.Fatalf("expected error for invalid version %q, got nil", bad)
			}
		})
	}
}

// TestNonReleaseBranch verifies that running on a branch that is not a
// release-vM.m.x branch is rejected.
func TestNonReleaseBranch(t *testing.T) {
	t.Parallel()
	testLogger()

	// scaffoldRepo uses "master" if we just pass a non-release name.
	tmpDir := scaffoldRepo(t, "main")

	err := run(false, "test-remote", true, tmpDir, "v2.9.0-rc.1", []string{}, []string{})
	if err == nil {
		t.Fatal("expected error for non-release branch, got nil")
	}
	if !strings.Contains(err.Error(), "release branch") {
		t.Errorf("error should mention release branch, got: %v", err)
	}
}

// TestVersionFileUpdate verifies that internal/version/version.go is updated to
// the target version as part of the single commit.
func TestVersionFileUpdate(t *testing.T) {
	t.Parallel()
	testLogger()

	const (
		branch  = "release-v2.9.x"
		version = "v2.9.9-rc.1"
	)

	tmpDir := scaffoldRepo(t, branch)

	if err := run(false, "test-remote", true, tmpDir, version, []string{}, []string{}); err != nil {
		t.Fatalf("autoreleasetagger failed: %v", err)
	}

	assertVersionFile(t, tmpDir, version)
}

// assertVersionFile checks that internal/version/version.go in tmpDir contains
// the expected version string in the var Tag line.
func assertVersionFile(t *testing.T, root, want string) {
	t.Helper()
	got, err := readVersionFile(root)
	if err != nil {
		t.Fatalf("failed to read version file: %v", err)
	}
	if got != want {
		t.Errorf("version file: got %q, want %q", got, want)
	}
}

// nonEmptyLines splits s on newlines and returns only non-empty entries.
func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// containsLine reports whether any trimmed line in s equals target.
func containsLine(s, target string) bool {
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) == target {
			return true
		}
	}
	return false
}

func runGitCommand(dir string, args ...string) error {
	var stderr bytes.Buffer
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("cmd failed: %w\n%s", err, stderr.String())
	}
	return nil
}

func runGitCommandWithOutput(dir string, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("cmd failed: %w\n%s", err, stderr.String())
	}
	return stdout.String(), nil
}

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

func copyTestdata(t *testing.T, src, dst string) {
	t.Helper()

	err := os.MkdirAll(filepath.Dir(dst), 0o755)
	if err != nil {
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

func TestAutoReleaseTaggerIntegration(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	defer os.RemoveAll(tmpDir)

	// Copy testdata to tmpDir.
	copyTestdata(t,
		filepath.Join("testdata", "root", "go.mod"),
		filepath.Join(tmpDir, "go.mod"),
	)
	copyTestdata(t,
		filepath.Join("testdata", "root", "go.work"),
		filepath.Join(tmpDir, "go.work"),
	)
	copyTestdata(t,
		filepath.Join("testdata", "root", "root.go"),
		filepath.Join(tmpDir, "root.go"),
	)

	os.MkdirAll(filepath.Join(tmpDir, "moduleA"), 0o755)
	copyTestdata(t,
		filepath.Join("testdata", "root", "moduleA", "go.mod"),
		filepath.Join(tmpDir, "moduleA", "go.mod"),
	)
	copyTestdata(t,
		filepath.Join("testdata", "root", "moduleA", "a.go"),
		filepath.Join(tmpDir, "moduleA", "a.go"),
	)
	os.MkdirAll(filepath.Join(tmpDir, "moduleB"), 0o755)
	copyTestdata(t,
		filepath.Join("testdata", "root", "moduleB", "go.mod"),
		filepath.Join(tmpDir, "moduleB", "go.mod"),
	)
	copyTestdata(t,
		filepath.Join("testdata", "root", "moduleB", "b.go"),
		filepath.Join(tmpDir, "moduleB", "b.go"),
	)

	// Initialize a Git repo in tmpDir
	if err := runGitCommand(tmpDir, "init"); err != nil {
		t.Fatalf("failed to git init: %v", err)
	}

	if err := runGitCommand(tmpDir, "add", "."); err != nil {
		t.Fatalf("failed to git add: %v", err)
	}

	// Add these lines to configure git.
	if err := runGitCommand(tmpDir, "config", "user.email", "test@example.com"); err != nil {
		t.Fatalf("failed to configure git user email: %v", err)
	}

	if err := runGitCommand(tmpDir, "config", "user.name", "Test User"); err != nil {
		t.Fatalf("failed to configure git user name: %v", err)
	}

	if err := runGitCommand(tmpDir, "commit", "-m", "initial commit"); err != nil {
		t.Fatalf("failed to git commit: %v", err)
	}

	var (
		version = "v0.1.0"
		dryRun  = false
		push    = false
		logger  = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	)
	slog.SetDefault(logger)
	if err := run(dryRun, "test-remote", !push, tmpDir, version, []string{}, []string{}); err != nil {
		t.Fatalf("autoreleasetagger failed: %v", err)
	}

	// Verify submodule commits.
	commits, err := runGitCommandWithOutput(tmpDir, "log", "--oneline")
	if err != nil {
		t.Fatalf("failed to read commits: %v", err)
	}

	commitA := fmt.Sprintf("moduleA: %s", version)
	if !strings.Contains(commits, commitA) {
		t.Errorf("expected commit for moduleA not found; commits:\n%s", commits)
	}

	commitB := fmt.Sprintf("moduleB: %s", version)
	if !strings.Contains(commits, commitB) {
		t.Errorf("expected commit for moduleB not found; commits:\n%s", commits)
	}

	// Verify tags.
	tags, err := runGitCommandWithOutput(tmpDir, "tag", "--list")
	if err != nil {
		t.Fatalf("failed to list git tags: %v", err)
	}

	if !strings.Contains(tags, version) {
		t.Errorf("expected tag %q not found; git tags:\n%s", version, tags)
	}

	tagA := fmt.Sprintf("moduleA/%s", version)
	if !strings.Contains(tags, tagA) {
		t.Errorf("expected submodule tag %q not found; git tags:\n%s", tagA, tags)
	}

	tagB := fmt.Sprintf("moduleB/%s", version)
	if !strings.Contains(tags, tagB) {
		t.Errorf("expected submodule tag %q not found; git tags:\n%s", tagB, tags)
	}
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

// runGitCommandWithOutput returns output of a Git command.
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

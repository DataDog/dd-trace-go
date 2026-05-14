// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// Package main_test contains integration tests for the autoreleasetagger tool.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// sharedBinary holds the path to the autoreleasetagger binary compiled once
// for the entire test run. All TestCI* tests use this instead of calling
// buildBinary(t) individually, which avoids 4+ redundant compilations and
// keeps the suite well within the 30-second budget.
var (
	sharedBinOnce sync.Once
	sharedBinPath string
	sharedBinErr  error
)

// getSharedBinary returns the path to the shared compiled binary, building it
// on the first call. It fatals the test if compilation fails.
func getSharedBinary(t *testing.T) string {
	t.Helper()
	sharedBinOnce.Do(func() {
		// Build into a directory that outlives individual test temp dirs.
		dir, err := os.MkdirTemp("", "autoreleasetagger-bin-*")
		if err != nil {
			sharedBinErr = fmt.Errorf("failed to create temp dir for binary: %w", err)
			return
		}
		sharedBinPath = filepath.Join(dir, "autoreleasetagger")
		cmd := exec.Command("go", "build", "-o", sharedBinPath, ".")
		cmd.Dir = "."
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			sharedBinErr = fmt.Errorf("go build failed: %w\n%s", err, stderr.String())
		}
	})
	if sharedBinErr != nil {
		t.Fatalf("shared binary unavailable: %v", sharedBinErr)
	}
	return sharedBinPath
}

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
	for _, sub := range []string{"moduleA", "moduleB", "moduleC"} {
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
// match the release branch is rejected with an invalid_branch error.
func TestVersionBranchMismatch(t *testing.T) {
	t.Parallel()
	testLogger()

	tmpDir := scaffoldRepo(t, "release-v2.9.x")

	err := run(false, "test-remote", true, tmpDir, "v2.8.0-rc.1", []string{}, []string{})
	if err == nil {
		t.Fatal("expected error for version/branch mismatch, got nil")
	}
	var se *StructuredError
	if !errors.As(err, &se) {
		t.Fatalf("expected *StructuredError, got %T: %v", err, err)
	}
	if se.Code != errInvalidBranch {
		t.Errorf("expected code %q, got %q", errInvalidBranch, se.Code)
	}
	if !strings.Contains(err.Error(), "mismatch") {
		t.Errorf("error should mention mismatch, got: %v", err)
	}
}

// TestInvalidVersion verifies that a malformed version string is rejected.
// Each subtest gets its own scaffolded repo to avoid sharing mutable state
// across parallel subtests.
func TestInvalidVersion(t *testing.T) {
	t.Parallel()
	testLogger()

	for _, bad := range []string{"2.9.0", "v2.9", "vX.Y.Z", "v2.9.0-beta.1"} {
		bad := bad
		t.Run(bad, func(t *testing.T) {
			t.Parallel()
			tmpDir := scaffoldRepo(t, "release-v2.9.x")
			err := run(false, "test-remote", true, tmpDir, bad, []string{}, []string{})
			if err == nil {
				t.Fatalf("expected error for invalid version %q, got nil", bad)
			}
		})
	}
}

// TestNonReleaseBranch verifies that running on a branch that is not a
// release-vM.m.x branch is rejected with an invalid_branch error.
func TestNonReleaseBranch(t *testing.T) {
	t.Parallel()
	testLogger()

	tmpDir := scaffoldRepo(t, "main")

	err := run(false, "test-remote", true, tmpDir, "v2.9.0-rc.1", []string{}, []string{})
	if err == nil {
		t.Fatal("expected error for non-release branch, got nil")
	}
	var se *StructuredError
	if !errors.As(err, &se) {
		t.Fatalf("expected *StructuredError, got %T: %v", err, err)
	}
	if se.Code != errInvalidBranch {
		t.Errorf("expected code %q, got %q", errInvalidBranch, se.Code)
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

// TestDirtyTree verifies that checkDirtyTree returns a dirty_tree
// StructuredError when the working tree has uncommitted changes in tracked
// files, and returns nil on a clean tree.
func TestDirtyTree(t *testing.T) {
	t.Parallel()
	testLogger()

	tmpDir := scaffoldRepo(t, "release-v2.9.x")

	// Clean tree should produce no error.
	if err := checkDirtyTree(tmpDir); err != nil {
		t.Fatalf("expected nil on clean tree, got: %v", err)
	}

	// Modify a tracked file.
	versionFile := filepath.Join(tmpDir, "internal", "version", "version.go")
	original, err := os.ReadFile(versionFile)
	if err != nil {
		t.Fatalf("failed to read version file: %v", err)
	}
	if err := os.WriteFile(versionFile, append(original, []byte("// dirty\n")...), 0o644); err != nil {
		t.Fatalf("failed to dirty version file: %v", err)
	}

	gotErr := checkDirtyTree(tmpDir)
	if gotErr == nil {
		t.Fatal("expected dirty_tree error, got nil")
	}
	var se *StructuredError
	if !errors.As(gotErr, &se) {
		t.Fatalf("expected *StructuredError, got %T: %v", gotErr, gotErr)
	}
	if se.Code != errDirtyTree {
		t.Errorf("expected code %q, got %q", errDirtyTree, se.Code)
	}
	files, ok := se.Details["modified_files"].([]string)
	if !ok || len(files) == 0 {
		t.Errorf("expected modified_files in details, got: %v", se.Details)
	}
}

// TestDirtyTreeUntrackedIgnored verifies that untracked files do not trigger
// the dirty-tree guard (editor swap files, build artefacts, etc.).
func TestDirtyTreeUntrackedIgnored(t *testing.T) {
	t.Parallel()
	testLogger()

	tmpDir := scaffoldRepo(t, "release-v2.9.x")

	// Create an untracked file.
	if err := os.WriteFile(filepath.Join(tmpDir, "editor.swp"), []byte("junk"), 0o644); err != nil {
		t.Fatalf("failed to create untracked file: %v", err)
	}

	if err := checkDirtyTree(tmpDir); err != nil {
		t.Errorf("untracked file should not trigger dirty_tree, got: %v", err)
	}
}

// TestTagsExistOnDifferentCommit verifies that checkTagsExist returns a
// tags_exist StructuredError when a tag exists but points at a commit other
// than HEAD, and returns nil when no conflicting tags exist.
func TestTagsExistOnDifferentCommit(t *testing.T) {
	t.Parallel()
	testLogger()

	const (
		branch  = "release-v2.9.x"
		version = "v2.9.9-rc.1"
	)

	tmpDir := scaffoldRepo(t, branch)

	// No tags yet — should be clean.
	if err := checkTagsExist(tmpDir, "", []string{version}); err != nil {
		t.Fatalf("expected nil when no tags exist, got: %v", err)
	}

	// Add an extra commit so the initial commit is no longer HEAD.
	extraFile := filepath.Join(tmpDir, "extra.txt")
	if err := os.WriteFile(extraFile, []byte("extra"), 0o644); err != nil {
		t.Fatalf("failed to write extra file: %v", err)
	}
	if err := runGitCommand(tmpDir, "add", "."); err != nil {
		t.Fatalf("git add failed: %v", err)
	}
	if err := runGitCommand(tmpDir, "commit", "-m", "extra commit"); err != nil {
		t.Fatalf("git commit failed: %v", err)
	}

	// Tag the first commit (now HEAD~1).
	if err := runGitCommand(tmpDir, "tag", "-am", version, version, "HEAD~1"); err != nil {
		t.Fatalf("failed to create tag: %v", err)
	}

	gotErr := checkTagsExist(tmpDir, "", []string{version})
	if gotErr == nil {
		t.Fatal("expected tags_exist error, got nil")
	}
	var se *StructuredError
	if !errors.As(gotErr, &se) {
		t.Fatalf("expected *StructuredError, got %T: %v", gotErr, gotErr)
	}
	if se.Code != errTagsExist {
		t.Errorf("expected code %q, got %q", errTagsExist, se.Code)
	}
	// Details must include the conflicting tag name.
	raw, _ := json.Marshal(se.Details)
	if !strings.Contains(string(raw), version) {
		t.Errorf("expected conflicting tag %q in details, got: %s", version, raw)
	}
}

// TestTagsExistAtHEADAllowed verifies that a tag pointing exactly at HEAD does
// NOT trigger the tags_exist guard (the idempotency path owns that case).
func TestTagsExistAtHEADAllowed(t *testing.T) {
	t.Parallel()
	testLogger()

	const (
		branch  = "release-v2.9.x"
		version = "v2.9.9-rc.1"
	)

	tmpDir := scaffoldRepo(t, branch)

	// Tag HEAD itself.
	if err := runGitCommand(tmpDir, "tag", "-am", version, version); err != nil {
		t.Fatalf("failed to create tag: %v", err)
	}

	// Should not be flagged as a conflict.
	if err := checkTagsExist(tmpDir, "", []string{version}); err != nil {
		t.Errorf("tag at HEAD should not trigger tags_exist, got: %v", err)
	}
}

// TestMultiCommitViolation verifies assertSingleCommit by creating a repo with
// two commits added after the baseline and confirming the guard fires, while a
// single new commit and zero new commits are both accepted.
func TestMultiCommitViolation(t *testing.T) {
	t.Parallel()
	testLogger()

	tmpDir := scaffoldRepo(t, "release-v2.9.x")

	// Record the baseline HEAD.
	baseline, err := runGitCommandWithOutput(tmpDir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("failed to get HEAD: %v", err)
	}
	baseline = strings.TrimSpace(baseline)

	// 0 new commits — must be accepted.
	if err := assertSingleCommit(tmpDir, baseline); err != nil {
		t.Errorf("expected nil for 0 new commits, got: %v", err)
	}

	// Add one commit.
	addCommit := func(msg string) {
		t.Helper()
		f := filepath.Join(tmpDir, msg+".txt")
		if err := os.WriteFile(f, []byte(msg), 0o644); err != nil {
			t.Fatalf("write failed: %v", err)
		}
		if err := runGitCommand(tmpDir, "add", "."); err != nil {
			t.Fatalf("git add failed: %v", err)
		}
		if err := runGitCommand(tmpDir, "commit", "-m", msg); err != nil {
			t.Fatalf("git commit failed: %v", err)
		}
	}

	addCommit("commit-one")

	// 1 new commit — must be accepted.
	if err := assertSingleCommit(tmpDir, baseline); err != nil {
		t.Errorf("expected nil for 1 new commit, got: %v", err)
	}

	addCommit("commit-two")

	// 2 new commits — must be rejected.
	gotErr := assertSingleCommit(tmpDir, baseline)
	if gotErr == nil {
		t.Fatal("expected multi_commit_violation, got nil")
	}
	var se *StructuredError
	if !errors.As(gotErr, &se) {
		t.Fatalf("expected *StructuredError, got %T: %v", gotErr, gotErr)
	}
	if se.Code != errMultiCommitViolation {
		t.Errorf("expected code %q, got %q", errMultiCommitViolation, se.Code)
	}
}

// TestPartialIdempotency verifies that when the version file and root tag are
// already correct but one contrib tag is missing, the tool treats the state as
// incomplete and repairs it (creates the missing tag) rather than exiting early
// as a no-op.
func TestPartialIdempotency(t *testing.T) {
	t.Parallel()
	testLogger()

	const (
		branch  = "release-v2.9.x"
		version = "v2.9.9-rc.1"
	)

	tmpDir := scaffoldRepo(t, branch)

	// Full first run.
	if err := run(false, "test-remote", true, tmpDir, version, []string{}, []string{}); err != nil {
		t.Fatalf("first run failed: %v", err)
	}

	// Manually delete one contrib tag to simulate a partial run.
	if err := runGitCommand(tmpDir, "tag", "-d", "moduleC/"+version); err != nil {
		t.Fatalf("failed to delete contrib tag: %v", err)
	}

	// Second run: must NOT exit as a no-op — it must repair the missing tag.
	if err := run(false, "test-remote", true, tmpDir, version, []string{}, []string{}); err != nil {
		t.Fatalf("repair run failed: %v", err)
	}

	// All tags must now exist at HEAD.
	tagsAtHead, err := runGitCommandWithOutput(tmpDir, "tag", "--points-at", "HEAD")
	if err != nil {
		t.Fatalf("failed to list tags at HEAD: %v", err)
	}
	for _, sub := range []string{"moduleA", "moduleB", "moduleC"} {
		tag := sub + "/" + version
		if !containsLine(tagsAtHead, tag) {
			t.Errorf("after repair, tag %q not found at HEAD:\n%s", tag, tagsAtHead)
		}
	}

	// Still exactly one release commit (repair must not add a second commit).
	commits, err := runGitCommandWithOutput(tmpDir, "log", "--oneline")
	if err != nil {
		t.Fatalf("failed to read commits: %v", err)
	}
	if got := len(nonEmptyLines(commits)); got != 2 {
		t.Errorf("expected 2 commits after repair, got %d:\n%s", got, commits)
	}
}

// TestTagsExistOnRemote verifies that checkTagsExist detects a conflicting tag
// that exists only on the remote (not locally), which would be invisible to a
// local-only git tag --list check.
func TestTagsExistOnRemote(t *testing.T) {
	t.Parallel()
	testLogger()

	const (
		branch  = "release-v2.9.x"
		version = "v2.9.9-rc.1"
	)

	// Create two repos: "remote" (bare) and "local" (clone of remote).
	remoteDir := t.TempDir()
	if err := runGitCommand(remoteDir, "init", "--bare"); err != nil {
		t.Fatalf("git init --bare failed: %v", err)
	}

	localDir := scaffoldRepo(t, branch)
	if err := runGitCommand(localDir, "remote", "add", "origin", remoteDir); err != nil {
		t.Fatalf("git remote add failed: %v", err)
	}

	// Push the initial commit to the remote so the remote is not empty.
	if err := runGitCommand(localDir, "push", "origin", branch); err != nil {
		t.Fatalf("git push failed: %v", err)
	}

	// Add a second commit locally so the initial commit is no longer HEAD.
	if err := os.WriteFile(filepath.Join(localDir, "extra.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	runGitCommand(localDir, "add", ".")                      //nolint:errcheck
	runGitCommand(localDir, "commit", "-m", "second commit") //nolint:errcheck

	// Create a tag on the first commit (HEAD~1) and push it to the remote.
	if err := runGitCommand(localDir, "tag", "-am", version, version, "HEAD~1"); err != nil {
		t.Fatalf("tag create failed: %v", err)
	}
	if err := runGitCommand(localDir, "push", "origin", version); err != nil {
		t.Fatalf("tag push failed: %v", err)
	}

	// Delete the local tag so it is only on the remote.
	if err := runGitCommand(localDir, "tag", "-d", version); err != nil {
		t.Fatalf("local tag delete failed: %v", err)
	}

	// A local-only check must NOT detect the conflict (local tag is gone).
	if err := checkTagsExist(localDir, "", []string{version}); err != nil {
		t.Errorf("local-only check should not see remote tag, got: %v", err)
	}

	// A check that includes the remote MUST detect the conflict.
	gotErr := checkTagsExist(localDir, "origin", []string{version})
	if gotErr == nil {
		t.Fatal("expected tags_exist error for remote tag, got nil")
	}
	var se *StructuredError
	if !errors.As(gotErr, &se) {
		t.Fatalf("expected *StructuredError, got %T: %v", gotErr, gotErr)
	}
	if se.Code != errTagsExist {
		t.Errorf("expected code %q, got %q", errTagsExist, se.Code)
	}
	raw, _ := json.Marshal(se.Details)
	if !strings.Contains(string(raw), "remote") {
		t.Errorf("expected source=remote in details, got: %s", raw)
	}
}

// TestDetachedHEAD verifies that validateVersionAndBranch returns an
// invalid_branch StructuredError when the checkout is in detached HEAD state,
// rather than silently misidentifying the branch.
func TestDetachedHEAD(t *testing.T) {
	t.Parallel()
	testLogger()

	tmpDir := scaffoldRepo(t, "release-v2.9.x")

	// Detach HEAD by checking out the commit hash directly.
	head, err := runGitCommandWithOutput(tmpDir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse failed: %v", err)
	}
	if err := runGitCommand(tmpDir, "checkout", strings.TrimSpace(head)); err != nil {
		t.Fatalf("detach HEAD failed: %v", err)
	}

	gotErr := validateVersionAndBranch(tmpDir, "v2.9.0-rc.1")
	if gotErr == nil {
		t.Fatal("expected error for detached HEAD, got nil")
	}
	var se *StructuredError
	if !errors.As(gotErr, &se) {
		t.Fatalf("expected *StructuredError, got %T: %v", gotErr, gotErr)
	}
	if se.Code != errInvalidBranch {
		t.Errorf("expected code %q, got %q", errInvalidBranch, se.Code)
	}
	if !strings.Contains(se.Msg, "detached") {
		t.Errorf("error message should mention detached HEAD, got: %s", se.Msg)
	}
}

// TestInvalidVersionStructuredError verifies that validateVersionAndBranch
// returns a *StructuredError with code invalid_version for bad version strings.
// Each subtest gets its own repo to avoid sharing mutable state across
// parallel subtests.
func TestInvalidVersionStructuredError(t *testing.T) {
	t.Parallel()
	testLogger()

	for _, bad := range []string{"2.9.0", "v2.9", "vX.Y.Z", "v2.9.0-beta.1"} {
		bad := bad
		t.Run(bad, func(t *testing.T) {
			t.Parallel()
			tmpDir := scaffoldRepo(t, "release-v2.9.x")
			err := validateVersionAndBranch(tmpDir, bad)
			if err == nil {
				t.Fatalf("expected error for %q, got nil", bad)
			}
			var se *StructuredError
			if !errors.As(err, &se) {
				t.Fatalf("expected *StructuredError, got %T: %v", err, err)
			}
			if se.Code != errInvalidVersion {
				t.Errorf("expected code %q, got %q", errInvalidVersion, se.Code)
			}
		})
	}
}

// TestInvalidBranchStructuredError verifies that validateVersionAndBranch
// returns a *StructuredError with code invalid_branch for branch-related
// problems (wrong branch name, major/minor mismatch, detached HEAD).
func TestInvalidBranchStructuredError(t *testing.T) {
	t.Parallel()
	testLogger()

	cases := []struct {
		name    string
		branch  string
		version string
		want    string // substring expected in the error message
	}{
		{"non-release-branch", "main", "v2.9.0-rc.1", "release branch"},
		{"major-minor-mismatch", "release-v2.9.x", "v2.8.0-rc.1", "mismatch"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tmpDir := scaffoldRepo(t, tc.branch)
			err := validateVersionAndBranch(tmpDir, tc.version)
			if err == nil {
				t.Fatalf("expected invalid_branch error, got nil")
			}
			var se *StructuredError
			if !errors.As(err, &se) {
				t.Fatalf("expected *StructuredError, got %T: %v", err, err)
			}
			if se.Code != errInvalidBranch {
				t.Errorf("expected code %q, got %q", errInvalidBranch, se.Code)
			}
			if !strings.Contains(se.Msg, tc.want) {
				t.Errorf("message should contain %q, got: %s", tc.want, se.Msg)
			}
		})
	}
}

// jsonErrorPayload mirrors the JSON shape emitted by renderError.
type jsonErrorPayload struct {
	Error   string         `json:"error"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// captureRenderError calls renderError with format=="json", captures what is
// written to stderr, and returns the parsed payload.
func captureRenderError(t *testing.T, err error) jsonErrorPayload {
	t.Helper()

	// Redirect os.Stderr to a pipe for the duration of this call.
	origStderr := os.Stderr
	r, w, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatalf("failed to create pipe: %v", pipeErr)
	}
	os.Stderr = w

	renderError(err, "json")

	w.Close()
	os.Stderr = origStderr

	var buf bytes.Buffer
	if _, copyErr := io.Copy(&buf, r); copyErr != nil {
		t.Fatalf("failed to read pipe: %v", copyErr)
	}

	var payload jsonErrorPayload
	if jsonErr := json.Unmarshal(buf.Bytes(), &payload); jsonErr != nil {
		t.Fatalf("failed to unmarshal JSON error: %v\nraw: %s", jsonErr, buf.String())
	}
	return payload
}

// TestJSONErrorFormatDirtyTree verifies the JSON shape for dirty_tree errors.
func TestJSONErrorFormatDirtyTree(t *testing.T) {
	t.Parallel()

	se := newStructuredError(
		errDirtyTree,
		"working tree has uncommitted changes in: internal/version/version.go",
		map[string]any{"modified_files": []string{"internal/version/version.go"}},
	)
	payload := captureRenderError(t, se)

	if payload.Error != string(errDirtyTree) {
		t.Errorf("error field: got %q, want %q", payload.Error, errDirtyTree)
	}
	if payload.Message == "" {
		t.Error("message field must not be empty")
	}
	if payload.Details == nil {
		t.Error("details field must not be nil for dirty_tree")
	}
}

// TestJSONErrorFormatTagsExist verifies the JSON shape for tags_exist errors.
func TestJSONErrorFormatTagsExist(t *testing.T) {
	t.Parallel()

	se := newStructuredError(
		errTagsExist,
		"tags already exist on a different commit: v2.9.0-rc.1@abc1234",
		map[string]any{"conflicts": []map[string]string{{"tag": "v2.9.0-rc.1", "commit": "abc1234"}}},
	)
	payload := captureRenderError(t, se)

	if payload.Error != string(errTagsExist) {
		t.Errorf("error field: got %q, want %q", payload.Error, errTagsExist)
	}
}

// TestJSONErrorFormatInvalidVersion verifies the JSON shape for invalid_version errors.
func TestJSONErrorFormatInvalidVersion(t *testing.T) {
	t.Parallel()

	se := newStructuredError(
		errInvalidVersion,
		`invalid version "badver": must match v<MAJOR>.<MINOR>.<PATCH>(-rc.<N>)?`,
		map[string]any{"version": "badver"},
	)
	payload := captureRenderError(t, se)

	if payload.Error != string(errInvalidVersion) {
		t.Errorf("error field: got %q, want %q", payload.Error, errInvalidVersion)
	}
}

// TestJSONErrorFormatInvalidBranch verifies the JSON shape for invalid_branch errors.
func TestJSONErrorFormatInvalidBranch(t *testing.T) {
	t.Parallel()

	se := newStructuredError(
		errInvalidBranch,
		`current branch "main" is not a release branch (expected release-v<MAJOR>.<MINOR>.x)`,
		map[string]any{"branch": "main"},
	)
	payload := captureRenderError(t, se)

	if payload.Error != string(errInvalidBranch) {
		t.Errorf("error field: got %q, want %q", payload.Error, errInvalidBranch)
	}
	if payload.Message == "" {
		t.Error("message field must not be empty")
	}
	if payload.Details == nil {
		t.Error("details field must not be nil for invalid_branch")
	}
}

// TestJSONErrorFormatInternalFallback verifies that a plain (non-structured)
// error is wrapped under the "internal" code when rendered as JSON.
func TestJSONErrorFormatInternalFallback(t *testing.T) {
	t.Parallel()

	plainErr := fmt.Errorf("something unexpected went wrong")
	payload := captureRenderError(t, plainErr)

	if payload.Error != string(errInternal) {
		t.Errorf("error field: got %q, want %q", payload.Error, errInternal)
	}
	if payload.Message != plainErr.Error() {
		t.Errorf("message field: got %q, want %q", payload.Message, plainErr.Error())
	}
}

// TestJSONErrorFormatTextMode verifies that human-readable mode omits JSON.
func TestJSONErrorFormatTextMode(t *testing.T) {
	t.Parallel()

	se := newStructuredError(errDirtyTree, "dirty", nil)

	origStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	renderError(se, "text")
	w.Close()
	os.Stderr = origStderr

	var buf bytes.Buffer
	io.Copy(&buf, r) //nolint:errcheck
	out := buf.String()

	// Must be prose, not JSON.
	if strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Errorf("text mode must not emit JSON, got: %s", out)
	}
	if !strings.Contains(out, "dirty") {
		t.Errorf("text mode must include the error message, got: %s", out)
	}
}

// buildBinary is retained for any test that needs a per-test binary path
// (e.g. to avoid races when tests modify the binary). Most TestCI* tests
// use getSharedBinary instead.
func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "autoreleasetagger")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = "."
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to build autoreleasetagger: %v\n%s", err, stderr.String())
	}
	return bin
}

// runBinary executes the autoreleasetagger binary with the given arguments
// from dir and returns stdout, stderr, and whether it exited successfully.
func runBinary(dir, bin string, args ...string) (stdout, stderr string, ok bool) {
	var outBuf, errBuf bytes.Buffer
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	return outBuf.String(), errBuf.String(), err == nil
}

// TestCIInvocation is the primary acceptance test: invoke the compiled
// binary with --format json on a clean repo and assert the resulting commit
// graph, tag set, and version file.
//
// We use v2.99.99-rc.1 (a clearly fictional patch/minor within the v2 major)
// so the testdata go.mod (example.com/root/v2) stays valid; the spec's
// "v9.9.9-rc.1" example assumes a matching major in the module path.
func TestCIInvocation(t *testing.T) {
	t.Parallel()

	const (
		branch  = "release-v2.99.x"
		version = "v2.99.99-rc.1"
	)

	bin := getSharedBinary(t)
	tmpDir := scaffoldRepo(t, branch)

	stdout, stderr, ok := runBinary(tmpDir, bin,
		"--version", version,
		"--format", "json",
		"--disable-push",
		"--root", tmpDir,
	)
	if !ok {
		t.Fatalf("binary exited non-zero\nstdout: %s\nstderr: %s", stdout, stderr)
	}

	// Exactly one new commit on top of the initial commit.
	commits, err := runGitCommandWithOutput(tmpDir, "log", "--oneline")
	if err != nil {
		t.Fatalf("failed to read commits: %v", err)
	}
	if got := len(nonEmptyLines(commits)); got != 2 {
		t.Errorf("expected 2 commits, got %d:\n%s", got, commits)
	}

	// All tags must point at HEAD.
	tags, err := runGitCommandWithOutput(tmpDir, "tag", "--points-at", "HEAD")
	if err != nil {
		t.Fatalf("failed to list tags: %v", err)
	}
	if !containsLine(tags, version) {
		t.Errorf("root tag %q not found at HEAD\ntags: %s", version, tags)
	}
	for _, sub := range []string{"moduleA", "moduleB", "moduleC"} {
		tag := sub + "/" + version
		if !containsLine(tags, tag) {
			t.Errorf("tag %q not found at HEAD\ntags: %s", tag, tags)
		}
	}

	// Version file must match.
	assertVersionFile(t, tmpDir, version)
}

// TestCIInvocationIdempotent verifies that running the binary twice with the
// same --version is a no-op on the second run (exit 0, no new commit).
func TestCIInvocationIdempotent(t *testing.T) {
	t.Parallel()

	const (
		branch  = "release-v2.99.x"
		version = "v2.99.99-rc.1"
	)

	bin := getSharedBinary(t)
	tmpDir := scaffoldRepo(t, branch)

	args := []string{"--version", version, "--format", "json", "--disable-push", "--root", tmpDir}

	if _, stderr, ok := runBinary(tmpDir, bin, args...); !ok {
		t.Fatalf("first run failed\nstderr: %s", stderr)
	}
	if _, stderr, ok := runBinary(tmpDir, bin, args...); !ok {
		t.Fatalf("second run (idempotent) failed\nstderr: %s", stderr)
	}

	commits, err := runGitCommandWithOutput(tmpDir, "log", "--oneline")
	if err != nil {
		t.Fatalf("failed to read commits: %v", err)
	}
	if got := len(nonEmptyLines(commits)); got != 2 {
		t.Errorf("expected 2 commits after idempotent run, got %d:\n%s", got, commits)
	}
}

// TestCIDirtyTreeError verifies that the binary exits non-zero and emits a
// dirty_tree JSON error when the working tree is dirty.
func TestCIDirtyTreeError(t *testing.T) {
	t.Parallel()

	const (
		branch  = "release-v2.99.x"
		version = "v2.99.99-rc.1"
	)

	bin := getSharedBinary(t)
	tmpDir := scaffoldRepo(t, branch)

	// Dirty a tracked file.
	vf := filepath.Join(tmpDir, "internal", "version", "version.go")
	orig, _ := os.ReadFile(vf)
	os.WriteFile(vf, append(orig, []byte("// dirty\n")...), 0o644) //nolint:errcheck

	_, stderrOut, ok := runBinary(tmpDir, bin,
		"--version", version,
		"--format", "json",
		"--disable-push",
		"--root", tmpDir,
	)
	if ok {
		t.Fatal("expected non-zero exit for dirty tree, got success")
	}
	assertJSONErrorCode(t, stderrOut, string(errDirtyTree))
}

// TestCITagsExistError verifies that the binary exits non-zero and emits a
// tags_exist JSON error when a target tag already exists on a different commit.
func TestCITagsExistError(t *testing.T) {
	t.Parallel()

	const (
		branch  = "release-v2.99.x"
		version = "v2.99.99-rc.1"
	)

	bin := getSharedBinary(t)
	tmpDir := scaffoldRepo(t, branch)

	// Add a second commit so we can tag the first.
	if err := os.WriteFile(filepath.Join(tmpDir, "extra.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	runGitCommand(tmpDir, "add", ".")             //nolint:errcheck
	runGitCommand(tmpDir, "commit", "-m", "bump") //nolint:errcheck

	// Tag the first commit (HEAD~1).
	if err := runGitCommand(tmpDir, "tag", "-am", version, version, "HEAD~1"); err != nil {
		t.Fatalf("tag create failed: %v", err)
	}

	_, stderrOut, ok := runBinary(tmpDir, bin,
		"--version", version,
		"--format", "json",
		"--disable-push",
		"--root", tmpDir,
	)
	if ok {
		t.Fatal("expected non-zero exit for conflicting tags, got success")
	}
	assertJSONErrorCode(t, stderrOut, string(errTagsExist))
}

// TestCIInvalidVersionError verifies that the binary exits non-zero and emits
// an invalid_version JSON error for a malformed version string.
func TestCIInvalidVersionError(t *testing.T) {
	t.Parallel()

	bin := getSharedBinary(t)
	tmpDir := scaffoldRepo(t, "release-v2.99.x")

	_, stderrOut, ok := runBinary(tmpDir, bin,
		"--version", "not-a-version",
		"--format", "json",
		"--disable-push",
		"--root", tmpDir,
	)
	if ok {
		t.Fatal("expected non-zero exit for invalid version, got success")
	}
	assertJSONErrorCode(t, stderrOut, string(errInvalidVersion))
}

// assertJSONErrorCode parses the JSON on stderr and asserts the "error" field
// matches the expected code.
func assertJSONErrorCode(t *testing.T, stderrOut, wantCode string) {
	t.Helper()
	var payload jsonErrorPayload
	if err := json.Unmarshal([]byte(strings.TrimSpace(stderrOut)), &payload); err != nil {
		t.Fatalf("failed to parse JSON error from stderr: %v\nraw stderr: %s", err, stderrOut)
	}
	if payload.Error != wantCode {
		t.Errorf("JSON error code: got %q, want %q\nfull payload: %s", payload.Error, wantCode, stderrOut)
	}
	if payload.Message == "" {
		t.Error("JSON error message must not be empty")
	}
}

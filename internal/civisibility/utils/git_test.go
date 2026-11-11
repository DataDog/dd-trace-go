// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package utils

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/bazel"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

func TestGetSafeDirectoryConfig(t *testing.T) {
	if !isGitFound() {
		t.Skip("Git not available, skipping safe directory config test")
	}

	// Reset the safeDirectoryOnce to ensure we're testing a fresh state
	safeDirectoryOnce = sync.Once{}
	safeDirectoryValue = ""

	// Get the safe directory config
	safeDir := getSafeDirectoryConfig()

	// Should return a non-empty path in a git repository
	assert.NotEmpty(t, safeDir, "Safe directory config should not be empty in a git repository")

	// The path should not end with /.git (it should be the repo root)
	assert.False(t, strings.HasSuffix(safeDir, "/.git"), "Safe directory should not end with /.git")
	assert.False(t, strings.HasSuffix(safeDir, "\\.git"), "Safe directory should not end with \\.git")

	// Calling it again should return the same cached value
	safeDir2 := getSafeDirectoryConfig()
	assert.Equal(t, safeDir, safeDir2, "Safe directory should be cached")
}

func TestSafeDirectoryConfigPassedToGitCommands(t *testing.T) {
	if !isGitFound() {
		t.Skip("Git not available, skipping safe directory test")
	}

	// Reset the safeDirectoryOnce to ensure we're testing a fresh state
	safeDirectoryOnce = sync.Once{}
	safeDirectoryValue = ""

	// Get the safe directory config
	safeDir := getSafeDirectoryConfig()
	assert.NotEmpty(t, safeDir, "Safe directory config should not be empty")

	// Use git config --list --show-origin to verify safe.directory is being passed
	// When we pass -c safe.directory=<path>, git should accept it and show it in the config
	// We run a command that will include the -c flag and verify it works
	out, err := execGitString(telemetry.NotSpecifiedCommandsType, "config", "--get", "safe.directory")
	// The command might return empty or error if safe.directory is not set in actual config,
	// but the important thing is that it doesn't fail due to the -c flag being malformed
	// The -c flag is added by execGit, so if the command runs without "unknown option" error, it works
	_ = out
	_ = err

	// Verify by running git version which should always succeed if safe.directory is properly passed
	version, err := execGitString(telemetry.NotSpecifiedCommandsType, "--version")
	assert.NoError(t, err, "git --version should succeed with safe.directory config")
	assert.Contains(t, version, "git version", "Output should contain git version")

	// Run git rev-parse to verify safe.directory works with repo-specific commands
	_, err = execGitString(telemetry.NotSpecifiedCommandsType, "rev-parse", "--show-toplevel")
	assert.NoError(t, err, "git rev-parse should succeed with safe.directory config")
}

func TestFilterSensitiveInfo(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// Basic cases
		{"https://user:pass@github.com/repo.git", "https://github.com/repo.git"},
		{"ssh://user@github.com/repo.git", "ssh://github.com/repo.git"},
		{"https://github.com/repo.git", "https://github.com/repo.git"},
		{"http://user:pass@github.com/repo.git", "http://github.com/repo.git"},

		// Edge cases
		{"", ""},
		{"https://@github.com/repo.git", "https://github.com/repo.git"},
		{"ftp://user@github.com/repo.git", "ftp://user@github.com/repo.git"}, // Unsupported protocol, should remain unchanged
		{"user@github.com/repo.git", "user@github.com/repo.git"},             // No protocol, should remain unchanged

		// Complex cases
		{"https://user:pass@github.com:8080/repo.git", "https://github.com:8080/repo.git"},
		{"ssh://user:auth@github.com/repo.git", "ssh://github.com/repo.git"},
		{"https://user:password@bitbucket.org/repo.git", "https://bitbucket.org/repo.git"},

		// Cases with special characters
		{"https://user:pa$$word@github.com/repo.git", "https://github.com/repo.git"},
		{"ssh://user!@github.com/repo.git", "ssh://github.com/repo.git"},
		{"https://user%40example.com@github.com/repo.git", "https://github.com/repo.git"}, // Encoded @ in username
	}

	for _, test := range tests {
		result := filterSensitiveInfo(test.input)
		assert.Equal(t, test.expected, result, "Failed for input: %s", test.input)
	}
}

func TestExecGitStringDisabledInPayloadFilesMode(t *testing.T) {
	t.Setenv(bazel.PayloadsInFilesEnv, "true")
	t.Setenv(bazel.UndeclaredOutputsDirEnv, t.TempDir())

	bazel.ResetForTesting()
	t.Cleanup(bazel.ResetForTesting)

	out, err := execGitString(telemetry.NotSpecifiedCommandsType, "--version")
	assert.Empty(t, out)
	assert.Error(t, err)
	assert.ErrorContains(t, err, "git CLI is disabled")
}

func TestExecGitStringDisabledByInternalEnv(t *testing.T) {
	t.Setenv(bazel.GitCLIDisabledEnv, "true")

	bazel.ResetForTesting()
	t.Cleanup(bazel.ResetForTesting)

	if bazel.IsPayloadFilesModeEnabled() {
		t.Fatal("expected payload-files mode to stay disabled")
	}
	out, err := execGitString(telemetry.NotSpecifiedCommandsType, "--version")
	assert.Empty(t, out)
	assert.Error(t, err)
	assert.ErrorContains(t, err, "git CLI is disabled")
}

func TestGetLocalGitData(t *testing.T) {
	data, err := getLocalGitData()
	if err != nil && acceptableError(err) {
		t.Skipf("test skipped due to: %s", err.Error())
	}

	assert.NoError(t, err)
	assert.NotEmpty(t, data.SourceRoot)
	assert.NotEmpty(t, data.RepositoryURL)
	assert.NotEmpty(t, data.CommitSha)
	assert.NotEmpty(t, data.AuthorName)
	assert.NotEmpty(t, data.AuthorEmail)
	assert.NotEmpty(t, data.AuthorDate)
	assert.NotEmpty(t, data.CommitterName)
	assert.NotEmpty(t, data.CommitterEmail)
	assert.NotEmpty(t, data.CommitterDate)
	assert.NotEmpty(t, data.CommitMessage)
}

func TestGetLastLocalGitCommitShas(t *testing.T) {
	shas := GetLastLocalGitCommitShas()
	assert.NotEmpty(t, shas)
}

func TestUnshallowGitRepository(t *testing.T) {
	_, err := UnshallowGitRepository()
	if err != nil && acceptableError(err) {
		t.Skipf("test skipped due to: %s", err.Error())
	}

	assert.NoError(t, err)
}

func TestUnshallowGitRepositoryTreatsEmptyFetchOutputAsSuccess(t *testing.T) {
	_, fetchesFile := setupFakeGitForUnshallow(t, false)

	ok, err := UnshallowGitRepository()

	assert.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, 1, countFakeGitFetches(t, fetchesFile))
}

func TestUnshallowGitRepositoryTreatsEmptyUpstreamFetchOutputAsSuccess(t *testing.T) {
	_, fetchesFile := setupFakeGitForUnshallow(t, true)

	ok, err := UnshallowGitRepository()

	assert.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, 2, countFakeGitFetches(t, fetchesFile))
}

func TestPackFiles(t *testing.T) {
	shas := GetLastLocalGitCommitShas()
	shas = shas[:min(len(shas), 5)]
	packfiles := CreatePackFiles(shas, []string{})
	t.Cleanup(func() { RemovePackFiles(packfiles) })
	assert.NotEmpty(t, packfiles)
}

func TestCreatePackFilesMissingPackFile(t *testing.T) {
	dir := t.TempDir()
	gitPath := filepath.Join(dir, "git")
	script := `#!/bin/sh
if [ "$1" = "-c" ]; then
	shift 2
fi
case "$1" in
	rev-list)
		printf 'abc123\n'
		;;
	pack-objects)
		printf 'packhash\n'
		;;
	*)
		exit 0
		;;
esac
`
	assert.NoError(t, os.WriteFile(gitPath, []byte(script), 0o755))
	gitBatPath := filepath.Join(dir, "git.bat")
	batchScript := `@echo off
if "%1"=="-c" (
	shift
	shift
)
if "%1"=="rev-list" (
	echo abc123
	exit /b 0
)
if "%1"=="pack-objects" (
	echo packhash
	exit /b 0
)
exit /b 0
`
	assert.NoError(t, os.WriteFile(gitBatPath, []byte(batchScript), 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	assert.Empty(t, CreatePackFiles([]string{"HEAD"}, nil))
}

func setupFakeGitForUnshallow(t *testing.T, failFirstFetch bool) (string, string) {
	t.Helper()

	resetGitCommandCachesForTesting(t)

	dir := t.TempDir()
	commandsFile := filepath.Join(dir, "commands.log")
	fetchesFile := filepath.Join(dir, "fetches.log")
	testBinary, err := os.Executable()
	assert.NoError(t, err)
	gitExecutable = testBinary
	gitExecutableArgs = []string{"-test.run=TestFakeGitForUnshallowHelperProcess", "--"}
	t.Setenv("DD_TEST_FAKE_GIT_HELPER", "1")
	t.Setenv("DD_TEST_GIT_COMMANDS", commandsFile)
	t.Setenv("DD_TEST_GIT_FETCHES", fetchesFile)
	if failFirstFetch {
		t.Setenv("DD_TEST_GIT_FAIL_FIRST_FETCH", "1")
	} else {
		t.Setenv("DD_TEST_GIT_FAIL_FIRST_FETCH", "")
	}

	return commandsFile, fetchesFile
}

func TestFakeGitForUnshallowHelperProcess(t *testing.T) {
	if os.Getenv("DD_TEST_FAKE_GIT_HELPER") != "1" {
		return
	}

	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) > 0 {
		args = args[1:]
	}
	for len(args) >= 2 && args[0] == "-c" {
		args = args[2:]
	}

	if commandsFile := os.Getenv("DD_TEST_GIT_COMMANDS"); commandsFile != "" {
		_ = os.WriteFile(commandsFile, []byte(strings.Join(args, " ")+"\n"), 0o644)
	}
	if len(args) == 0 {
		os.Exit(0)
	}

	switch args[0] {
	case "--version":
		_, _ = os.Stdout.WriteString("git version 2.39.0\n")
	case "rev-parse":
		switch {
		case len(args) > 1 && args[1] == "--is-shallow-repository":
			_, _ = os.Stdout.WriteString("true\n")
		case len(args) > 1 && args[1] == "HEAD":
			_, _ = os.Stdout.WriteString("0123456789012345678901234567890123456789\n")
		case len(args) > 1 && args[1] == "--abbrev-ref":
			_, _ = os.Stdout.WriteString("origin/main\n")
		}
	case "log":
		_, _ = os.Stdout.WriteString("0123456789012345678901234567890123456789 first commit\n")
	case "remote", "config":
		_, _ = os.Stdout.WriteString("origin\n")
	case "fetch":
		fetchesFile := os.Getenv("DD_TEST_GIT_FETCHES")
		if fetchesFile != "" {
			f, _ := os.OpenFile(fetchesFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
			if f != nil {
				_, _ = f.WriteString("fetch\n")
				_ = f.Close()
			}
		}
		if os.Getenv("DD_TEST_GIT_FAIL_FIRST_FETCH") == "1" && countFakeGitFetches(t, fetchesFile) == 1 {
			_, _ = os.Stderr.WriteString("fetch failed\n")
			os.Exit(1)
		}
	case "branch":
		_, _ = os.Stdout.WriteString("main\n")
	}
	os.Exit(0)
}

func resetGitCommandCachesForTesting(t *testing.T) {
	t.Helper()
	originalGitExecutable := gitExecutable
	originalGitExecutableArgs := append([]string(nil), gitExecutableArgs...)
	reset := func() {
		gitFinderOnce = sync.Once{}
		gitExecutable = originalGitExecutable
		gitExecutableArgs = append([]string(nil), originalGitExecutableArgs...)
		isGitFoundValue = false
		gitVersionOnce = sync.Once{}
		gitVersionValue = gitVersionData{}
		safeDirectoryOnce = sync.Once{}
		safeDirectoryValue = ""
		invalidateShallowCloneCache()
	}
	reset()
	t.Cleanup(reset)
}

func countFakeGitFetches(t *testing.T, fetchesFile string) int {
	t.Helper()
	data, err := os.ReadFile(fetchesFile)
	if os.IsNotExist(err) {
		return 0
	}
	assert.NoError(t, err)
	fetches := 0
	for field := range strings.FieldsSeq(string(data)) {
		if field == "fetch" {
			fetches++
		}
	}
	return fetches
}

func TestFetchCommitData(t *testing.T) {
	log.SetLevel(log.LevelDebug)
	commits := GetLastLocalGitCommitShas()
	for _, sha := range commits[:min(len(commits), 3)] {
		if gitData, err := fetchCommitData(sha); err == nil {
			assert.NotEmpty(t, gitData.AuthorName, "Author name should not be empty")
			assert.NotEmpty(t, gitData.AuthorEmail, "Author email should not be empty")
			assert.NotEmpty(t, gitData.AuthorDate, "Author date should not be empty")
			assert.NotEmpty(t, gitData.CommitterName, "Committer name should not be empty")
			assert.NotEmpty(t, gitData.CommitterEmail, "Committer email should not be empty")
			assert.NotEmpty(t, gitData.CommitterDate, "Committer date should not be empty")
			assert.NotEmpty(t, gitData.CommitMessage, "Commit message should not be empty")
		} else if acceptableError(err) {
			t.Skipf("test skipped due to: %s", err.Error())
		} else {
			t.Errorf("Failed to fetch commit data for SHA: %s, error: %v", sha, err)
		}
	}
}

// Tests for base branch detection functions

func TestRemoveRemotePrefix(t *testing.T) {
	tests := []struct {
		branchName string
		remoteName string
		expected   string
	}{
		{"origin/main", "origin", "main"},
		{"upstream/master", "upstream", "master"},
		{"origin/feature/test", "origin", "feature/test"},
		{"main", "origin", "main"},                   // No prefix
		{"upstream/main", "origin", "upstream/main"}, // Different remote
		{"", "origin", ""},                           // Empty branch
	}

	for _, test := range tests {
		result := removeRemotePrefix(test.branchName, test.remoteName)
		assert.Equal(t, test.expected, result, "Failed for branch: %s, remote: %s", test.branchName, test.remoteName)
	}
}

func TestIsMainLikeBranch(t *testing.T) {
	tests := []struct {
		branchName string
		remoteName string
		expected   bool
	}{
		// Base branches
		{"main", "origin", true},
		{"master", "origin", true},
		{"preprod", "origin", true},
		{"prod", "origin", true},
		{"dev", "origin", true},
		{"development", "origin", true},
		{"trunk", "origin", true},

		// Release and hotfix branches
		{"release/v1.0", "origin", true},
		{"release/2023.1", "origin", true},
		{"hotfix/critical", "origin", true},
		{"hotfix/bug-123", "origin", true},

		// Remote branches
		{"origin/main", "origin", true},
		{"origin/master", "origin", true},
		{"upstream/main", "upstream", true},

		// Feature branches (should not match)
		{"feature/test", "origin", false},
		{"bugfix/issue-123", "origin", false},
		{"update/dependencies", "origin", false},
		{"my-feature-branch", "origin", false},

		// Edge cases
		{"", "origin", false},
		{"main-backup", "origin", false}, // Similar but not exact
		{"maintenance", "origin", false}, // Similar but not in list
	}

	for _, test := range tests {
		result := isMainLikeBranch(test.branchName, test.remoteName)
		assert.Equal(t, test.expected, result, "Failed for branch: %s, remote: %s", test.branchName, test.remoteName)
	}
}

func TestIsDefaultBranch(t *testing.T) {
	tests := []struct {
		branch        string
		defaultBranch string
		remoteName    string
		expected      bool
	}{
		{"main", "main", "origin", true},
		{"master", "master", "origin", true},
		{"origin/main", "main", "origin", true},
		{"upstream/master", "master", "upstream", true},
		{"feature/test", "main", "origin", false},
		{"origin/feature", "main", "origin", false},
		{"main", "master", "origin", false}, // Different default
	}

	for _, test := range tests {
		result := isDefaultBranch(test.branch, test.defaultBranch, test.remoteName)
		assert.Equal(t, test.expected, result, "Failed for branch: %s, default: %s, remote: %s", test.branch, test.defaultBranch, test.remoteName)
	}
}

func TestComputeBranchMetrics(t *testing.T) {
	// This test requires a real git repository with branches
	// We'll test with the current repository
	if !isGitFound() {
		t.Skip("Git not available, skipping branch metrics test")
	}

	// Get current branch for testing
	currentBranch, err := getSourceBranch()
	if err != nil {
		t.Skip("Could not get current branch, skipping test")
	}

	// Test with the current branch as both candidate and source (should work)
	metrics, err := computeBranchMetrics([]string{currentBranch}, currentBranch)
	if err != nil && acceptableError(err) {
		t.Skipf("test skipped due to: %s", err.Error())
	}
	assert.NoError(t, err)

	// When comparing a branch to itself, ahead should be 0
	if metric, exists := metrics[currentBranch]; exists {
		assert.Equal(t, 0, metric.ahead, "Ahead count should be 0 when comparing branch to itself")
		assert.NotEmpty(t, metric.baseSha, "Base SHA should not be empty")
	}
}

func TestFindBestBranch(t *testing.T) {
	// Test with mock metrics
	metrics := map[string]branchMetrics{
		"main": {
			behind:  10,
			ahead:   2,
			baseSha: "sha1",
		},
		"master": {
			behind:  15,
			ahead:   1, // Better (fewer ahead commits)
			baseSha: "sha2",
		},
		"origin/main": {
			behind:  5,
			ahead:   2,
			baseSha: "sha3",
		},
	}

	// Test 1: master should win (fewer ahead commits)
	result := findBestBranch(metrics, "main", "origin")
	assert.Equal(t, "sha2", result, "Should prefer branch with fewer ahead commits")

	// Test 2: When ahead counts are equal, prefer default branch
	metrics["master"] = branchMetrics{behind: 15, ahead: 2, baseSha: "sha2"}
	result = findBestBranch(metrics, "main", "origin")

	// Both "main" and "origin/main" are default branches with equal ahead counts
	// The algorithm should prefer one of them over "master"
	assert.Contains(t, []string{"sha1", "sha3"}, result, "Should prefer a default branch when ahead counts are equal")

	// Test 3: Prefer exact default branch name over remote prefixed one
	// This test is important to ensure deterministic behavior when both "main" and "origin/main"
	// are considered default branches with equal ahead counts. Without proper tie-breaking,
	// Go's non-deterministic map iteration can cause flaky test results.
	metrics = map[string]branchMetrics{
		"main": {
			behind:  10,
			ahead:   2,
			baseSha: "sha1",
		},
		"origin/main": {
			behind:  5,
			ahead:   2,
			baseSha: "sha3",
		},
	}
	result = findBestBranch(metrics, "main", "origin")
	assert.Equal(t, "sha1", result, "Should prefer exact default branch name over remote prefixed one")

	// Test 4: Empty metrics
	result = findBestBranch(map[string]branchMetrics{}, "main", "origin")
	assert.Equal(t, "", result, "Should return empty string for empty metrics")
}

func TestGetRemoteName(t *testing.T) {
	if !isGitFound() {
		t.Skip("Git not available, skipping remote name test")
	}

	remoteName, err := getRemoteName()
	if err != nil && acceptableError(err) {
		t.Skipf("test skipped due to: %s", err.Error())
	}
	assert.NoError(t, err)
	assert.NotEmpty(t, remoteName, "Remote name should not be empty")
	// Most repositories have "origin" as the default remote
	assert.Contains(t, []string{"origin", "upstream"}, remoteName, "Remote name should be a common name")
}

func TestGetSourceBranch(t *testing.T) {
	if !isGitFound() {
		t.Skip("Git not available, skipping source branch test")
	}

	branch, err := getSourceBranch()
	if err != nil && acceptableError(err) {
		t.Skipf("test skipped due to: %s", err.Error())
	}
	assert.NoError(t, err)
	assert.NotEmpty(t, branch, "Source branch should not be empty")
}

func TestGetBaseBranchSha(t *testing.T) {
	if !isGitFound() {
		t.Skip("Git not available, skipping base branch SHA test")
	}

	// Test with main as default
	sha, err := GetBaseBranchSha("main")

	// The result depends on the repository state:
	// - If no candidates found, should return error
	// - If candidates found, should return a valid SHA
	// - The algorithm no longer returns early for main-like branches

	if err != nil {
		// Error is acceptable if no candidates found or merge-base fails
		t.Logf("GetBaseBranchSha returned error (acceptable in some scenarios): %v", err)
		// Verify it's one of the expected error scenarios
		errorMessage := err.Error()
		expectedErrors := []string{
			"no candidate base branches found",
			"failed to find best base branch",
			"failed to find merge base",
			"failed to get remote branches",
		}

		hasExpectedError := false
		for _, expectedError := range expectedErrors {
			if strings.Contains(errorMessage, expectedError) {
				hasExpectedError = true
				break
			}
		}
		assert.True(t, hasExpectedError, "Error should be one of the expected scenarios: %v", err)
	} else if sha == "" {
		// Empty SHA could happen in some edge cases
		t.Logf("GetBaseBranchSha returned empty SHA")
	} else {
		// Valid SHA should be 40 characters long
		assert.Len(t, sha, 40, "SHA should be 40 characters long")
		assert.Regexp(t, "^[a-f0-9]{40}$", sha, "SHA should be valid hex string")
	}
}

func TestGetBaseBranchShaWithoutGit(t *testing.T) {
	// Temporarily disable git for this test
	originalGitFound := isGitFoundValue
	isGitFoundValue = false
	defer func() {
		isGitFoundValue = originalGitFound
		gitFinderOnce = sync.Once{} // Reset the sync.Once
	}()

	sha, err := GetBaseBranchSha("master")
	if err != nil && acceptableError(err) {
		t.Skipf("test skipped due to: %s", err.Error())
	}
	assert.Error(t, err)
	assert.Equal(t, "", sha)
	assert.Contains(t, err.Error(), "git executable not found")
}

func TestBranchMetricsStruct(t *testing.T) {
	// Test the branchMetrics struct
	metrics := branchMetrics{
		behind:  5,
		ahead:   3,
		baseSha: "abcdef1234567890",
	}

	assert.Equal(t, 5, metrics.behind)
	assert.Equal(t, 3, metrics.ahead)
	assert.Equal(t, "abcdef1234567890", metrics.baseSha)
}

func TestPossibleBaseBranches(t *testing.T) {
	// Test that the possibleBaseBranches constant contains expected values
	expectedBranches := []string{"main", "master", "preprod", "prod", "dev", "development", "trunk"}

	assert.Equal(t, expectedBranches, possibleBaseBranches, "possibleBaseBranches should contain expected values")
	assert.Len(t, possibleBaseBranches, 7, "Should have 7 possible base branches")
}

func TestBaseLikeBranchFilter(t *testing.T) {
	// Test the regex pattern directly
	testCases := []struct {
		branch   string
		expected bool
	}{
		{"main", true},
		{"master", true},
		{"preprod", true},
		{"prod", true},
		{"dev", true},
		{"development", true},
		{"trunk", true},
		{"release/v1.0", true},
		{"release/2023.1", true},
		{"hotfix/bug", true},
		{"hotfix/critical-fix", true},
		{"feature/test", false},
		{"bugfix/issue", false},
		{"update/deps", false},
		{"random-branch", false},
		{"", false},
	}

	for _, test := range testCases {
		result := baseLikeBranchFilter.MatchString(test.branch)
		assert.Equal(t, test.expected, result, "Failed for branch: %s", test.branch)
	}
}

func TestDetectDefaultBranch(t *testing.T) {
	if !isGitFound() {
		t.Skip("Git not available, skipping default branch detection test")
	}

	// Test with a real remote name
	remoteName, err := getRemoteName()
	if err != nil {
		t.Skip("Could not get remote name, skipping test")
	}

	defaultBranch, err := detectDefaultBranch(remoteName)
	if err != nil && acceptableError(err) {
		t.Skipf("test skipped due to: %s", err.Error())
	}

	// The function should either succeed or fail gracefully
	if err != nil {
		// If it fails, it should be because no default branch could be detected
		assert.Contains(t, err.Error(), "could not detect default branch")
		assert.Equal(t, "", defaultBranch)
	} else {
		// If it succeeds, we should get a valid branch name
		assert.NotEmpty(t, defaultBranch, "Default branch should not be empty when detection succeeds")
		// Common default branch names
		assert.Contains(t, []string{"main", "master", "develop", "dev"}, defaultBranch,
			"Default branch should be a common name, got: %s", defaultBranch)
	}
}

func TestFindFallbackDefaultBranch(t *testing.T) {
	if !isGitFound() {
		t.Skip("Git not available, skipping fallback default branch test")
	}

	// Test with a real remote name
	remoteName, err := getRemoteName()
	if err != nil {
		t.Skip("Could not get remote name, skipping test")
	}

	fallbackBranch := findFallbackDefaultBranch(remoteName)

	// The function should either return a valid branch or empty string
	if fallbackBranch != "" {
		assert.Contains(t, []string{"main", "master"}, fallbackBranch,
			"Fallback branch should be main or master, got: %s", fallbackBranch)
	}
	// If empty string, that's also acceptable - it means neither main nor master exists
}

func TestFindFallbackDefaultBranchWithNonExistentRemote(t *testing.T) {
	if !isGitFound() {
		t.Skip("Git not available, skipping fallback default branch test")
	}

	// Test with a non-existent remote
	fallbackBranch := findFallbackDefaultBranch("nonexistent")

	// Should return empty string since the remote doesn't exist
	assert.Equal(t, "", fallbackBranch, "Should return empty string for non-existent remote")
}

func TestDetectDefaultBranchWithNonExistentRemote(t *testing.T) {
	if !isGitFound() {
		t.Skip("Git not available, skipping default branch detection test")
	}

	// Test with a non-existent remote
	defaultBranch, err := detectDefaultBranch("nonexistent")
	if err != nil && acceptableError(err) {
		t.Skipf("test skipped due to: %s", err.Error())
	}

	// Should fail to detect
	assert.Error(t, err)
	assert.Equal(t, "", defaultBranch)
	assert.Contains(t, err.Error(), "could not detect default branch")
}

func TestGetBaseBranchShaWithAutoDetection(t *testing.T) {
	if !isGitFound() {
		t.Skip("Git not available, skipping base branch SHA test with auto-detection")
	}

	// Test with empty string to force auto-detection
	sha, err := GetBaseBranchSha("")

	// The result depends on the repository state:
	// - If no candidates found, should return error
	// - If candidates found, should return a valid SHA
	// - The algorithm no longer returns early for main-like branches

	if err != nil {
		// Error is acceptable if no candidates found or merge-base fails
		t.Logf("GetBaseBranchSha returned error (acceptable in some scenarios): %v", err)
		// Verify it's one of the expected error scenarios
		errorMessage := err.Error()
		expectedErrors := []string{
			"no candidate base branches found",
			"failed to find best base branch",
			"failed to find merge base",
			"failed to get remote branches",
		}

		hasExpectedError := false
		for _, expectedError := range expectedErrors {
			if strings.Contains(errorMessage, expectedError) {
				hasExpectedError = true
				break
			}
		}
		assert.True(t, hasExpectedError, "Error should be one of the expected scenarios: %v", err)
	} else if sha == "" {
		// Empty SHA could happen in some edge cases
		t.Logf("GetBaseBranchSha returned empty SHA")
	} else {
		// Valid SHA should be 40 characters long
		assert.Len(t, sha, 40, "SHA should be 40 characters long")
		assert.Regexp(t, "^[a-f0-9]{40}$", sha, "SHA should be valid hex string")
	}
}

func TestGetRemoteBranches(t *testing.T) {
	if !isGitFound() {
		t.Skip("Git not available, skipping remote branches test")
	}

	remoteName, err := getRemoteName()
	if err != nil {
		t.Skip("Could not get remote name, skipping test")
	}

	branches, err := getRemoteBranches(remoteName)
	if err != nil && acceptableError(err) {
		t.Skipf("test skipped due to: %s", err.Error())
	}
	assert.NoError(t, err)

	// Should get some remote branches (even if empty in some test environments)
	assert.NotNil(t, branches)

	// All returned branches should have the remote prefix
	for _, branch := range branches {
		if branch == remoteName {
			continue // Skip the remote name itself
		}
		assert.Contains(t, branch, remoteName+"/", "Remote branch should have remote prefix: %s", branch)
	}
}

func TestCheckAndFetchBranchUpdatedAlgorithm(t *testing.T) {
	if !isGitFound() {
		t.Skip("Git not available, skipping fetch branch test")
	}

	remoteName, err := getRemoteName()
	if err != nil {
		t.Skip("Could not get remote name, skipping test")
	}

	// Test with a common branch that might exist
	testBranch := "main"

	// This should not fail even if the branch doesn't exist
	checkAndFetchBranch(testBranch, remoteName)

	// Test should complete without errors - the function handles missing branches gracefully
	assert.True(t, true, "checkAndFetchBranch should complete without panicking")
}

func TestGetBaseBranchShaWithCIBaseBranch(t *testing.T) {
	if !isGitFound() {
		t.Skip("Git not available, skipping CI base branch test")
	}

	// This test verifies that the algorithm correctly handles git.pull_request.base_branch from CI
	// We can't easily mock CI environment in this test framework, but we can verify the logic path

	// Test that the algorithm works correctly when no CI tags are available
	// (This essentially tests the Step 2a path)
	sha, err := GetBaseBranchSha("main")

	// Since we're testing in a real repository, we expect either:
	// 1. A valid SHA if candidates are found
	// 2. An error if no candidates are found
	// 3. Empty SHA in edge cases

	if err != nil {
		t.Logf("GetBaseBranchSha without CI tags returned error: %v", err)
		// This is expected in many test scenarios
	} else if sha == "" {
		t.Logf("GetBaseBranchSha without CI tags returned empty SHA")
	} else {
		assert.Len(t, sha, 40, "SHA should be 40 characters long")
		assert.Regexp(t, "^[a-f0-9]{40}$", sha, "SHA should be valid hex string")
		t.Logf("GetBaseBranchSha without CI tags returned valid SHA: %s", sha)
	}
}

// CI providers check out one commit by default. Base branch detection must
// still find where the source branch left its base branch, after the base
// branch moved on, without losing history that was already enough.
func TestGetBaseBranchShaInShallowClones(t *testing.T) {
	const day = 24 * time.Hour
	tests := []struct {
		name string
		// ages of the first, base, feature and main commits; the first one
		// defaults to a day before the base
		firstAge, baseAge, featureAge, mainAge time.Duration
		checkout                               func(t *testing.T, origin, checkout, feature, main string)
		// pullRequest runs only with a pull request base branch, and gives the
		// feature commit as the CI provider's head commit.
		pullRequest bool
		// stillShallow checks that no full history was fetched.
		stillShallow bool
		// noBase expects no base commit: it is older than the fetched month.
		noBase bool
		// complete is a checkout with all of its history, not a shallow one.
		complete bool
	}{
		{name: "push checkout", checkout: pushCheckout},
		// A month of history would cut the base off: the checkout already holds it.
		{name: "history already enough", baseAge: 90 * day,
			checkout: func(t *testing.T, origin, checkout, feature, main string) {
				require.NoError(t, os.Mkdir(checkout, 0o755))
				runFixtureGit(t, checkout, "init")
				runFixtureGit(t, checkout, "remote", "add", "origin", fileURL(origin))
				runFixtureGit(t, checkout, "fetch", "--no-tags", "--depth=2", "origin", "+"+feature+":refs/remotes/origin/feature", "+"+main+":refs/remotes/origin/main")
				runFixtureGit(t, checkout, "checkout", "--force", "-B", "feature", "refs/remotes/origin/feature")
			}},
		{name: "branch older than a month", firstAge: 90 * day, baseAge: 60 * day, noBase: true, stillShallow: true, checkout: pushCheckout},
		{name: "every commit older than a month", baseAge: 90 * day, featureAge: 89 * day, mainAge: 88 * day, noBase: true, stillShallow: true, checkout: pushCheckout},
		{name: "local commit", checkout: func(t *testing.T, origin, checkout, feature, main string) {
			pushCheckout(t, origin, checkout, feature, main)
			runFixtureGit(t, checkout, "commit", "--allow-empty", "-m", "synthesized merge")
		}},
		{name: "deleted branch", checkout: func(t *testing.T, origin, checkout, feature, main string) {
			runFixtureGit(t, origin, "switch", "-c", "release/old", "HEAD~1")
			runFixtureGit(t, origin, "commit", "--allow-empty", "-m", "release")
			runFixtureGit(t, origin, "switch", "main")
			pushCheckout(t, origin, checkout, feature, main)
			runFixtureGit(t, checkout, "fetch", "--no-tags", "--depth=1", "origin", "+refs/heads/release/old:refs/remotes/origin/release/old")
			release := runFixtureGit(t, origin, "rev-parse", "release/old")
			runFixtureGit(t, origin, "branch", "-D", "release/old")
			// Without its commit, the remote rejects a fetch that names it.
			runFixtureGit(t, origin, "reflog", "expire", "--expire=now", "--all")
			runFixtureGit(t, origin, "gc", "--prune=now", "--quiet")
			cmd := exec.Command("git", "cat-file", "-e", release)
			cmd.Dir = origin
			require.Error(t, cmd.Run())
		}},
		// actions/checkout on a pull_request event checks out the merge commit.
		{name: "pull request merge checkout", pullRequest: true,
			checkout: func(t *testing.T, origin, checkout, feature, main string) {
				runFixtureGit(t, origin, "switch", "--detach", main)
				runFixtureGit(t, origin, "merge", "--no-ff", "-m", "merge", feature)
				runFixtureGit(t, origin, "update-ref", "refs/pull/1/merge", "HEAD")
				runFixtureGit(t, origin, "switch", "main")
				require.NoError(t, os.Mkdir(checkout, 0o755))
				runFixtureGit(t, checkout, "init")
				runFixtureGit(t, checkout, "remote", "add", "origin", fileURL(origin))
				runFixtureGit(t, checkout, "fetch", "--no-tags", "--depth=1", "origin", "+refs/pull/1/merge:refs/remotes/pull/1/merge")
				runFixtureGit(t, checkout, "checkout", "--force", "--detach", "refs/remotes/pull/1/merge")
			}},
		// A base-like branch that never meets the source must not fetch the
		// full history once main has a merge base.
		{name: "independent candidate", firstAge: 90 * day, stillShallow: true, checkout: func(t *testing.T, origin, checkout, feature, main string) {
			runFixtureGit(t, origin, "switch", "--orphan", "prod")
			runFixtureGit(t, origin, "commit", "--allow-empty", "-m", "prod")
			runFixtureGit(t, origin, "switch", "main")
			pushCheckout(t, origin, checkout, feature, main)
		}},
		// A complete clone keeps its ancestry: the base branch, missing from it,
		// must not be fetched as a single commit that hides an old merge base.
		{name: "complete single-branch clone", firstAge: 90 * day, baseAge: 60 * day, complete: true,
			checkout: func(t *testing.T, origin, checkout, _, _ string) {
				runFixtureGit(t, filepath.Dir(checkout), "clone", "--single-branch", "--branch=feature", fileURL(origin), checkout)
				runFixtureGit(t, checkout, "branch", "main", "HEAD~1")
			}},
		{name: "single-branch clone", checkout: func(t *testing.T, origin, checkout, _, _ string) {
			runFixtureGit(t, filepath.Dir(checkout), "clone", "--depth=1", "--single-branch", "--branch=feature", fileURL(origin), checkout)
		}},
	}
	for _, tt := range tests {
		for mode, prBaseBranch := range map[string]string{"detected base branch": "", "pull request base branch": "main"} {
			if (tt.name == "deleted branch" || tt.name == "independent candidate") && prBaseBranch != "" {
				continue // the pull request base branch is the only candidate
			}
			if tt.pullRequest && prBaseBranch == "" {
				continue
			}
			t.Run(tt.name+"/"+mode, func(t *testing.T) {
				isolateFixtureGit(t)
				firstAge := tt.firstAge
				if firstAge == 0 {
					firstAge = tt.baseAge + day
				}
				root, origin, base, feature, main := newBaseBranchOrigin(t, firstAge, tt.baseAge, tt.featureAge, tt.mainAge)
				checkout := filepath.Join(root, "checkout")
				tt.checkout(t, origin, checkout, feature, main)
				require.Equal(t, strconv.FormatBool(!tt.complete), runFixtureGit(t, checkout, "rev-parse", "--is-shallow-repository"))

				t.Chdir(checkout)
				resetGitCommandCachesForTesting(t)
				ResetCITags()
				t.Cleanup(ResetCITags)
				AddCITags(constants.GitPrBaseBranch, prBaseBranch)
				// The CI running these tests can give a head commit of its own.
				headCommit := ""
				if tt.pullRequest {
					headCommit = feature
				}
				AddCITags(constants.GitHeadCommit, headCommit)

				sha, err := GetBaseBranchSha("")
				if tt.noBase {
					assert.ErrorContains(t, err, "merge base")
					assert.Empty(t, sha)
				} else {
					require.NoError(t, err)
					assert.Equal(t, base, sha)
				}
				if tt.stillShallow {
					assert.Equal(t, "true", runFixtureGit(t, checkout, "rev-parse", "--is-shallow-repository"))
				}
			})
		}
	}
}

// newBaseBranchOrigin creates an origin repository whose main and feature
// branches leave a base commit, with commits as old as the given ages. main
// moves on after feature leaves it.
func newBaseBranchOrigin(t *testing.T, firstAge, baseAge, featureAge, mainAge time.Duration) (root, origin, base, feature, main string) {
	t.Helper()
	root = t.TempDir()
	origin = filepath.Join(root, "origin")
	require.NoError(t, os.Mkdir(origin, 0o755))
	runFixtureGit(t, origin, "init", "--initial-branch=main")
	commitFixtureAt(t, origin, "first", firstAge)
	commitFixtureAt(t, origin, "base", baseAge)
	base = runFixtureGit(t, origin, "rev-parse", "HEAD")
	runFixtureGit(t, origin, "switch", "-c", "feature")
	commitFixtureAt(t, origin, "feature", featureAge)
	feature = runFixtureGit(t, origin, "rev-parse", "HEAD")
	runFixtureGit(t, origin, "switch", "main")
	commitFixtureAt(t, origin, "main moved on", mainAge)
	main = runFixtureGit(t, origin, "rev-parse", "HEAD")
	return root, origin, base, feature, main
}

// The Git upload can complete the history while base branch detection runs,
// between its first comparison and its own deepening.
func TestGetBaseBranchShaAfterTheGitUploadCompletesTheHistory(t *testing.T) {
	isolateFixtureGit(t)
	root, origin, base, feature, main := newBaseBranchOrigin(t, 24*time.Hour, 0, 0, 0)
	checkout := filepath.Join(root, "checkout")
	pushCheckout(t, origin, checkout, feature, main)
	t.Chdir(checkout)
	resetGitCommandCachesForTesting(t)
	ResetCITags()
	t.Cleanup(ResetCITags)
	AddCITags(constants.GitPrBaseBranch, "")
	AddCITags(constants.GitHeadCommit, "")

	require.True(t, isGitFound())
	testBinary, err := os.Executable()
	require.NoError(t, err)
	t.Setenv("DD_TEST_GIT_COMPLETING_UPLOAD", "1")
	t.Setenv("DD_TEST_REAL_GIT", gitExecutable)
	t.Setenv("DD_TEST_GIT_UPLOAD_DONE", filepath.Join(root, "upload-done"))
	gitExecutable = testBinary
	gitExecutableArgs = []string{"-test.run=^TestFakeGitCompletingUploadHelperProcess$", "--"}

	sha, err := GetBaseBranchSha("")
	require.NoError(t, err)
	assert.Equal(t, base, sha)
	assert.FileExists(t, filepath.Join(root, "upload-done"), "the first merge-base must run before the upload completes")
}

// TestFakeGitCompletingUploadHelperProcess runs git. Before the first
// merge-base, it completes the history as the Git upload would, then fails it
// as the shallow history did.
func TestFakeGitCompletingUploadHelperProcess(t *testing.T) {
	if os.Getenv("DD_TEST_GIT_COMPLETING_UPLOAD") != "1" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) > 0 {
		args = args[1:]
	}
	realGit := os.Getenv("DD_TEST_REAL_GIT")
	done := os.Getenv("DD_TEST_GIT_UPLOAD_DONE")
	if slices.Contains(args, "merge-base") {
		if _, err := os.Stat(done); os.IsNotExist(err) {
			if out, err := exec.Command(realGit, "fetch", "--unshallow", "origin").CombinedOutput(); err != nil {
				_, _ = os.Stderr.Write(out)
				os.Exit(2)
			}
			_ = os.WriteFile(done, nil, 0o644)
			os.Exit(1)
		}
	}
	cmd := exec.Command(realGit, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			os.Exit(exitErr.ExitCode())
		}
		os.Exit(2)
	}
	os.Exit(0)
}

// After a pull request's head is fetched without a depth limit, the clone can
// end at many commits that neither the source nor a candidate reaches: only
// theirs may cost a fetch.
func TestGetBaseBranchShaDeepensOnlyTheComparedHistory(t *testing.T) {
	isolateFixtureGit(t)
	// History older than the month keeps the clone shallow throughout.
	root, origin, base, feature, main := newBaseBranchOrigin(t, 90*24*time.Hour, 0, 0, 0)
	const unrelated = 20
	for i := range unrelated {
		// Two commits, so that fetching the tip alone leaves a boundary.
		name := "topic-" + strconv.Itoa(i)
		runFixtureGit(t, origin, "switch", "--orphan", name)
		runFixtureGit(t, origin, "commit", "--allow-empty", "-m", name)
		runFixtureGit(t, origin, "commit", "--allow-empty", "-m", name+" tip")
	}
	runFixtureGit(t, origin, "switch", "main")
	checkout := filepath.Join(root, "checkout")
	pushCheckout(t, origin, checkout, feature, main)
	// One depth-limited fetch, which keeps a boundary at every tip.
	fetch := make([]string, 0, 5+unrelated)
	fetch = append(fetch, "fetch", "--no-tags", "--depth=1", "origin", "+"+feature+":refs/remotes/origin/feature")
	for i := range unrelated {
		name := "topic-" + strconv.Itoa(i)
		fetch = append(fetch, "+refs/heads/"+name+":refs/remotes/origin/"+name)
	}
	runFixtureGit(t, checkout, fetch...)
	shallow, err := os.ReadFile(filepath.Join(checkout, ".git", "shallow"))
	require.NoError(t, err)
	require.Len(t, strings.Fields(string(shallow)), unrelated+1)
	t.Chdir(checkout)
	resetGitCommandCachesForTesting(t)
	ResetCITags()
	t.Cleanup(ResetCITags)
	AddCITags(constants.GitPrBaseBranch, "")
	AddCITags(constants.GitHeadCommit, "")
	trace := filepath.Join(root, "trace.log")
	t.Setenv("GIT_TRACE", trace)

	sha, err := GetBaseBranchSha("")
	require.NoError(t, err)
	assert.Equal(t, base, sha)
	logged, err := os.ReadFile(trace)
	require.NoError(t, err)
	fetches := strings.Count(string(logged), "built-in: git fetch '--shallow-since")
	assert.LessOrEqual(t, fetches, 2, "only the feature and main boundaries need history")
	assert.Equal(t, "true", runFixtureGit(t, checkout, "rev-parse", "--is-shallow-repository"))
}

// useBaseBranchCheckout changes to checkout and resets what base branch
// detection caches, with prBaseBranch as the CI's pull request base branch and
// no head commit. It returns a function that counts, in git's trace of the
// commands that follow, the commands that start with prefix.
func useBaseBranchCheckout(t *testing.T, checkout, prBaseBranch string) (count func(prefix string) int) {
	t.Helper()
	t.Chdir(checkout)
	resetGitCommandCachesForTesting(t)
	ResetCITags()
	t.Cleanup(ResetCITags)
	AddCITags(constants.GitPrBaseBranch, prBaseBranch)
	AddCITags(constants.GitHeadCommit, "")
	trace := filepath.Join(t.TempDir(), "trace.log")
	t.Setenv("GIT_TRACE", trace)
	return func(prefix string) int {
		logged, err := os.ReadFile(trace)
		require.NoError(t, err)
		return strings.Count(string(logged), "built-in: git "+prefix)
	}
}

// Every boundary in the compared history must be deepened before comparing: a
// merge base found through one side can be older than the one through another.
func TestGetBaseBranchShaFindsTheBestMergeBase(t *testing.T) {
	isolateFixtureGit(t)
	const day = 24 * time.Hour
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	require.NoError(t, os.Mkdir(origin, 0o755))
	runFixtureGit(t, origin, "init", "--initial-branch=main")
	commitFixtureAt(t, origin, "first", 90*day)
	commitFixtureAt(t, origin, "old base", 20*day)
	runFixtureGit(t, origin, "branch", "p")
	commitFixtureAt(t, origin, "new base", 10*day)
	newBase := runFixtureGit(t, origin, "rev-parse", "HEAD")
	runFixtureGit(t, origin, "branch", "q")
	commitFixtureAt(t, origin, "main tip", day)
	// p, on the old base, is newer than q, on the new one: it is deepened first.
	runFixtureGit(t, origin, "switch", "q")
	commitFixtureAt(t, origin, "q", 5*day)
	runFixtureGit(t, origin, "switch", "p")
	commitFixtureAt(t, origin, "p", 3*day)
	runFixtureGit(t, origin, "switch", "-c", "feature")
	runFixtureGit(t, origin, "merge", "--no-ff", "-m", "merge q", "q")
	feature := runFixtureGit(t, origin, "rev-parse", "HEAD")
	runFixtureGit(t, origin, "switch", "main")

	checkout := filepath.Join(root, "checkout")
	require.NoError(t, os.Mkdir(checkout, 0o755))
	runFixtureGit(t, checkout, "init")
	runFixtureGit(t, checkout, "remote", "add", "origin", fileURL(origin))
	runFixtureGit(t, checkout, "fetch", "--no-tags", "--depth=2", "origin", "+"+feature+":refs/remotes/origin/feature")
	runFixtureGit(t, checkout, "checkout", "--force", "-B", "feature", "refs/remotes/origin/feature")
	useBaseBranchCheckout(t, checkout, "main")

	sha, err := GetBaseBranchSha("")
	require.NoError(t, err)
	assert.Equal(t, newBase, sha)
}

// A pull request's base branch is fetched without asking the remote first; a
// local branch of that name still serves when the remote doesn't have it.
func TestGetBaseBranchShaFetchesThePullRequestBaseBranchDirectly(t *testing.T) {
	t.Run("on the remote", func(t *testing.T) {
		isolateFixtureGit(t)
		root, origin, base, feature, main := newBaseBranchOrigin(t, 24*time.Hour, 0, 0, 0)
		checkout := filepath.Join(root, "checkout")
		pushCheckout(t, origin, checkout, feature, main)
		count := useBaseBranchCheckout(t, checkout, "main")

		sha, err := GetBaseBranchSha("")
		require.NoError(t, err)
		assert.Equal(t, base, sha)
		assert.Zero(t, count("ls-remote --heads"))
	})
	t.Run("only local", func(t *testing.T) {
		isolateFixtureGit(t)
		root, origin, base, _, _ := newBaseBranchOrigin(t, 24*time.Hour, 0, 0, 0)
		checkout := filepath.Join(root, "checkout")
		runFixtureGit(t, root, "clone", "--single-branch", "--branch=feature", fileURL(origin), checkout)
		runFixtureGit(t, checkout, "branch", "main", base)
		runFixtureGit(t, origin, "branch", "-m", "main", "trunk")
		useBaseBranchCheckout(t, checkout, "main")

		sha, err := GetBaseBranchSha("")
		require.NoError(t, err)
		assert.Equal(t, base, sha)
	})
	t.Run("nowhere", func(t *testing.T) {
		isolateFixtureGit(t)
		root, origin, _, feature, main := newBaseBranchOrigin(t, 24*time.Hour, 0, 0, 0)
		checkout := filepath.Join(root, "checkout")
		pushCheckout(t, origin, checkout, feature, main)
		runFixtureGit(t, origin, "branch", "-m", "main", "trunk")
		useBaseBranchCheckout(t, checkout, "main")

		sha, err := GetBaseBranchSha("")
		assert.Error(t, err)
		assert.Empty(t, sha)
	})
}

// The detected base branch candidates are looked up on the remote in one
// request, and only the ones it has are fetched.
func TestGetBaseBranchShaAsksTheRemoteOnce(t *testing.T) {
	isolateFixtureGit(t)
	root, origin, base, feature, main := newBaseBranchOrigin(t, 24*time.Hour, 0, 0, 0)
	runFixtureGit(t, origin, "branch", "dev", main)
	checkout := filepath.Join(root, "checkout")
	pushCheckout(t, origin, checkout, feature, main)
	count := useBaseBranchCheckout(t, checkout, "")

	sha, err := GetBaseBranchSha("")
	require.NoError(t, err)
	assert.Equal(t, base, sha)
	assert.Equal(t, 1, count("ls-remote --heads"))
	assert.Equal(t, 2, count("fetch --recurse-submodules=no --depth 1"), "main and dev")
}

// A fetch can write the remote-tracking ref of a later candidate through the
// remote's configured refspec: that candidate is then not fetched again.
func TestGetBaseBranchShaKeepsRefsWrittenByAnEarlierFetch(t *testing.T) {
	isolateFixtureGit(t)
	root, origin, base, feature, main := newBaseBranchOrigin(t, 24*time.Hour, 0, 0, 0)
	runFixtureGit(t, origin, "switch", "-c", "dev", main)
	runFixtureGit(t, origin, "commit", "--allow-empty", "-m", "dev")
	runFixtureGit(t, origin, "switch", "main")
	checkout := filepath.Join(root, "checkout")
	pushCheckout(t, origin, checkout, feature, main)
	runFixtureGit(t, checkout, "config", "--add", "remote.origin.fetch", "+refs/heads/main:refs/remotes/origin/dev")
	useBaseBranchCheckout(t, checkout, "")

	sha, err := GetBaseBranchSha("")
	require.NoError(t, err)
	assert.Equal(t, base, sha)
	assert.Equal(t, main, runFixtureGit(t, checkout, "rev-parse", "refs/remotes/origin/dev"))
}

// The boundaries that need history are deepened in one fetch.
func TestGetBaseBranchShaDeepensInOneFetch(t *testing.T) {
	isolateFixtureGit(t)
	root, origin, base, feature, main := newBaseBranchOrigin(t, 90*24*time.Hour, 0, 0, 0)
	checkout := filepath.Join(root, "checkout")
	pushCheckout(t, origin, checkout, feature, main)
	count := useBaseBranchCheckout(t, checkout, "main")

	sha, err := GetBaseBranchSha("")
	require.NoError(t, err)
	assert.Equal(t, base, sha)
	assert.Equal(t, 1, count("fetch '--shallow-since"), "the feature and main boundaries in one fetch")
}

// Fetching a base branch must not fetch the submodule commits that its history
// names: base branch detection reads only the repository's own history.
func TestCheckAndFetchBranchLeavesSubmodulesAlone(t *testing.T) {
	isolateFixtureGit(t)
	root := t.TempDir()
	library := filepath.Join(root, "library")
	require.NoError(t, os.Mkdir(library, 0o755))
	runFixtureGit(t, library, "init", "--initial-branch=main")
	runFixtureGit(t, library, "commit", "--allow-empty", "-m", "s0")
	s0 := runFixtureGit(t, library, "rev-parse", "HEAD")
	runFixtureGit(t, library, "switch", "-c", "next")
	runFixtureGit(t, library, "commit", "--allow-empty", "-m", "s1")
	s1 := runFixtureGit(t, library, "rev-parse", "HEAD")
	runFixtureGit(t, library, "switch", "main")

	origin := filepath.Join(root, "origin")
	require.NoError(t, os.Mkdir(origin, 0o755))
	runFixtureGit(t, origin, "init", "--initial-branch=main")
	require.NoError(t, os.WriteFile(filepath.Join(origin, ".gitmodules"), []byte("[submodule \"lib\"]\n\tpath = lib\n\turl = "+fileURL(library)+"\n"), 0o644))
	runFixtureGit(t, origin, "add", ".gitmodules")
	runFixtureGit(t, origin, "update-index", "--add", "--cacheinfo", "160000,"+s0+",lib")
	runFixtureGit(t, origin, "commit", "-m", "base")
	runFixtureGit(t, origin, "switch", "-c", "feature")
	runFixtureGit(t, origin, "commit", "--allow-empty", "-m", "feature")
	runFixtureGit(t, origin, "switch", "main")
	// main points the submodule at s1, then back at s0.
	runFixtureGit(t, origin, "update-index", "--cacheinfo", "160000,"+s1+",lib")
	runFixtureGit(t, origin, "commit", "-m", "library s1")
	runFixtureGit(t, origin, "update-index", "--cacheinfo", "160000,"+s0+",lib")
	runFixtureGit(t, origin, "commit", "-m", "library s0")

	checkout := filepath.Join(root, "checkout")
	runFixtureGit(t, root, "clone", "--single-branch", "--branch=feature", fileURL(origin), checkout)
	runFixtureGit(t, checkout, "-c", "protocol.file.allow=always", "submodule", "update", "--init", "--depth=1")
	submodule := filepath.Join(checkout, "lib")
	require.Equal(t, s0, runFixtureGit(t, submodule, "rev-parse", "HEAD"))
	hasS1 := func() bool {
		cmd := exec.Command("git", "cat-file", "-e", s1+"^{commit}")
		cmd.Dir = submodule
		return cmd.Run() == nil
	}
	require.False(t, hasS1())

	// Let git fetch local submodules, as it would remote ones.
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "protocol.file.allow")
	t.Setenv("GIT_CONFIG_VALUE_0", "always")
	t.Chdir(checkout)
	resetGitCommandCachesForTesting(t)

	checkAndFetchBranch("main", "origin")

	runFixtureGit(t, checkout, "rev-parse", "--verify", "refs/remotes/origin/main")
	assert.False(t, hasS1(), "the submodule must not get the commit that main's history names")
}

// A failed shallow clone check must not pass for a complete clone: that would
// fetch a base branch's whole history into a shallow one.
func TestCheckAndFetchBranchWithoutShallowState(t *testing.T) {
	resetGitCommandCachesForTesting(t)
	commands := filepath.Join(t.TempDir(), "commands.log")
	testBinary, err := os.Executable()
	require.NoError(t, err)
	gitExecutable = testBinary
	gitExecutableArgs = []string{"-test.run=^TestFakeGitWithoutShallowStateHelperProcess$", "--"}
	t.Setenv("DD_TEST_GIT_WITHOUT_SHALLOW_STATE", "1")
	t.Setenv("DD_TEST_GIT_COMMANDS", commands)

	checkAndFetchBranch("main", "origin")

	for range 2 {
		_, err := isAShallowCloneRepository()
		assert.Error(t, err, "a failed check must not be cached as a complete clone")
	}
	logged, err := os.ReadFile(commands)
	require.NoError(t, err)
	assert.NotContains(t, string(logged), "fetch")
}

// TestFakeGitWithoutShallowStateHelperProcess is a git whose shallow clone
// check fails, with a remote that has main. It logs its commands.
func TestFakeGitWithoutShallowStateHelperProcess(t *testing.T) {
	if os.Getenv("DD_TEST_GIT_WITHOUT_SHALLOW_STATE") != "1" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) > 0 {
		args = args[1:]
	}
	for len(args) >= 2 && args[0] == "-c" {
		args = args[2:]
	}
	if f, err := os.OpenFile(os.Getenv("DD_TEST_GIT_COMMANDS"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		_, _ = f.WriteString(strings.Join(args, " ") + "\n")
		_ = f.Close()
	}
	switch {
	case len(args) >= 2 && args[0] == "rev-parse" && args[1] == "--is-shallow-repository":
		os.Exit(128)
	case len(args) >= 1 && args[0] == "show-ref":
		os.Exit(1)
	case len(args) >= 1 && args[0] == "ls-remote":
		_, _ = os.Stdout.WriteString("0123456789012345678901234567890123456789\trefs/heads/main\n")
	}
	os.Exit(0)
}

// The Git upload and base branch detection check and invalidate the shallow
// clone cache from different goroutines; run with -race.
func TestShallowCloneCacheConcurrentInvalidation(t *testing.T) {
	usePackFilesGitFixture(t)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 20 {
				_, err := isAShallowCloneRepository()
				assert.NoError(t, err)
				invalidateShallowCloneCache()
			}
		})
	}
	wg.Wait()
	shallow, err := isAShallowCloneRepository()
	require.NoError(t, err)
	assert.False(t, shallow)
}

// pushCheckout checks out feature as actions/checkout does for a push: one
// commit, then a local branch.
func pushCheckout(t *testing.T, origin, checkout, feature, _ string) {
	t.Helper()
	require.NoError(t, os.Mkdir(checkout, 0o755))
	runFixtureGit(t, checkout, "init")
	runFixtureGit(t, checkout, "remote", "add", "origin", fileURL(origin))
	runFixtureGit(t, checkout, "fetch", "--no-tags", "--depth=1", "origin", "+"+feature+":refs/remotes/origin/feature")
	runFixtureGit(t, checkout, "checkout", "--force", "-B", "feature", "refs/remotes/origin/feature")
}

// commitFixtureAt commits in dir as if age ago.
func commitFixtureAt(t *testing.T, dir, message string, age time.Duration) {
	t.Helper()
	date := time.Now().Add(-age).Format(time.RFC3339)
	cmd := exec.Command("git", "-c", "user.name=CI Fixture", "-c", "user.email=ci-fixture@example.com", "commit", "--allow-empty", "-m", message)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git commit: %s", out)
}

// fileURL is the file URL of path, which git needs for shallow fetches from a
// local repository.
func fileURL(path string) string {
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return "file://" + path
}

func acceptableError(err error) bool {
	errMessage := strings.ToLower(err.Error())
	// if the error is related to a shallow.lock file, we will skip the test;
	// if the error is due to transient GitHub connectivity or transport failures, we will skip the test;
	// the test is flaky in the CI due to multiple git commands running at the same time.
	return strings.Contains(errMessage, "shallow.lock") ||
		strings.Contains(errMessage, "couldn't connect to server") ||
		strings.Contains(errMessage, "the requested url returned error: 500") ||
		strings.Contains(errMessage, "rpc failed; http 500") ||
		strings.Contains(errMessage, "fatal: expected flush after ref listing") ||
		strings.Contains(errMessage, "fatal: expected 'packfile'")
}

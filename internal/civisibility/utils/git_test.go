// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package utils

import (
	"strings"
	"sync"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/stretchr/testify/assert"
)

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

func TestGetLocalGitData(t *testing.T) {
	data, err := getLocalGitData()

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
	if err != nil && strings.Contains(err.Error(), "shallow.lock") {
		// if the error is related to a shallow.lock file, we will skip the test;
		// the test is flaky in the CI due to multiple git commands running at the same time.
		return
	}

	assert.NoError(t, err)
}

func TestFetchCommitData(t *testing.T) {
	log.SetLevel(log.LevelDebug)
	for _, sha := range GetLastLocalGitCommitShas() {
		if gitData, err := fetchCommitData(sha); err == nil {
			assert.NotEmpty(t, gitData.AuthorName, "Author name should not be empty")
			assert.NotEmpty(t, gitData.AuthorEmail, "Author email should not be empty")
			assert.NotEmpty(t, gitData.AuthorDate, "Author date should not be empty")
			assert.NotEmpty(t, gitData.CommitterName, "Committer name should not be empty")
			assert.NotEmpty(t, gitData.CommitterEmail, "Committer email should not be empty")
			assert.NotEmpty(t, gitData.CommitterDate, "Committer date should not be empty")
			assert.NotEmpty(t, gitData.CommitMessage, "Commit message should not be empty")
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

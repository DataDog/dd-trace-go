// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package utils

import (
	"sync"
	"testing"

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
	assert.NoError(t, err)
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

func TestGetLocalBranches(t *testing.T) {
	if !isGitFound() {
		t.Skip("Git not available, skipping local branches test")
	}

	branches, err := getLocalBranches("origin")
	assert.NoError(t, err)
	assert.NotEmpty(t, branches, "Should have at least one branch")

	// Verify no empty strings in the result
	for _, branch := range branches {
		assert.NotEmpty(t, branch, "Branch name should not be empty")
	}
}

func TestGetBaseBranchSha(t *testing.T) {
	if !isGitFound() {
		t.Skip("Git not available, skipping base branch SHA test")
	}

	// Test with master as default
	sha, err := GetBaseBranchSha("main")

	// The result depends on the repository state:
	// - If current branch is already a base-like branch, should return empty
	// - If no candidates found, should return error
	// - If candidates found, should return a valid SHA

	if err != nil {
		// Error is acceptable if we're on a base branch or no candidates found
		t.Logf("GetBaseBranchSha returned error (acceptable): %v", err)
	} else if sha == "" {
		// Empty SHA is acceptable if we're on a base branch
		t.Logf("GetBaseBranchSha returned empty SHA (current branch is likely a base branch)")
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
		gitFinder = sync.Once{} // Reset the sync.Once
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

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package utils

import (
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

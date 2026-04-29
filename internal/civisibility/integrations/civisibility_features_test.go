// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package integrations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSearchCommitsResponseMissingCommitsPreservesLocalOrder(t *testing.T) {
	response := newSearchCommitsResponse(
		[]string{"local-1", "remote-1", "local-2", "remote-2", "local-3"},
		[]string{"remote-2", "remote-1"},
		true,
	)

	assert.Equal(t, []string{"local-1", "local-2", "local-3"}, response.missingCommits())
}

func TestSearchCommitsResponseMissingCommitsKeepsMissingDuplicates(t *testing.T) {
	response := newSearchCommitsResponse(
		[]string{"missing-1", "remote-1", "missing-1", "missing-2"},
		[]string{"remote-1"},
		true,
	)

	assert.Equal(t, []string{"missing-1", "missing-1", "missing-2"}, response.missingCommits())
}

func TestSearchCommitsResponseMissingCommitsReturnsEmptyWhenAllLocalCommitsAreRemote(t *testing.T) {
	response := newSearchCommitsResponse(
		[]string{"remote-1", "remote-2"},
		[]string{"remote-2", "remote-1"},
		true,
	)

	assert.Empty(t, response.missingCommits())
}

func TestUploadRepositoryChangesSkipsUploadWhenNoCommitsAreMissing(t *testing.T) {
	resetCIVisibilityStateForTesting()

	getSearchCommitsFunc = func() (*searchCommitsResponse, error) {
		return newSearchCommitsResponse(
			[]string{"remote-1", "remote-2"},
			[]string{"remote-2", "remote-1"},
			true,
		), nil
	}
	unshallowGitRepositoryFunc = func() (bool, error) {
		t.Fatal("unshallow should not run when all commits are already known")
		return false, nil
	}
	sendObjectsPackFileFunc = func(_ string, _ []string, _ []string) (int64, error) {
		t.Fatal("packfile upload should not run when all commits are already known")
		return 0, nil
	}

	bytes, err := uploadRepositoryChanges()

	require.NoError(t, err)
	assert.Zero(t, bytes)
}

func TestUploadRepositoryChangesReusesInitialMissingCommitsWhenUnshallowIsUnavailable(t *testing.T) {
	resetCIVisibilityStateForTesting()

	getSearchCommitsFunc = func() (*searchCommitsResponse, error) {
		return newSearchCommitsResponse(
			[]string{"local-1", "remote-1", "local-2"},
			[]string{"remote-1"},
			true,
		), nil
	}
	unshallowGitRepositoryFunc = func() (bool, error) {
		return false, nil
	}

	var uploadedCommit string
	var uploadedIncludes []string
	var uploadedExcludes []string
	sendObjectsPackFileFunc = func(commitSha string, commitsToInclude []string, commitsToExclude []string) (int64, error) {
		uploadedCommit = commitSha
		uploadedIncludes = append([]string(nil), commitsToInclude...)
		uploadedExcludes = append([]string(nil), commitsToExclude...)
		return 42, nil
	}

	bytes, err := uploadRepositoryChanges()

	require.NoError(t, err)
	assert.Equal(t, int64(42), bytes)
	assert.Equal(t, "local-1", uploadedCommit)
	assert.Equal(t, []string{"local-1", "local-2"}, uploadedIncludes)
	assert.Equal(t, []string{"remote-1"}, uploadedExcludes)
}

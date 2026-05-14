// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package integrations

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	civisibilitynet "github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
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

func TestUploadRepositoryChangesUsesHookSnapshot(t *testing.T) {
	resetCIVisibilityStateForTesting()
	t.Cleanup(resetCIVisibilityStateForTesting)

	uploadStarted := make(chan struct{})
	uploadRelease := make(chan struct{})
	getSearchCalls := 0

	getSearchCommitsFunc = func() (*searchCommitsResponse, error) {
		getSearchCalls++
		if getSearchCalls == 1 {
			close(uploadStarted)
			<-uploadRelease
			return newSearchCommitsResponse([]string{"local-1"}, nil, true), nil
		}
		return newSearchCommitsResponse([]string{"local-1"}, []string{"local-1"}, true), nil
	}
	unshallowGitRepositoryFunc = func() (bool, error) {
		return true, nil
	}
	sendObjectsPackFileFunc = func(_ string, _ []string, _ []string) (int64, error) {
		return 42, nil
	}

	done := make(chan struct{})
	var bytes int64
	var err error
	go func() {
		defer close(done)
		bytes, err = uploadRepositoryChanges()
	}()

	<-uploadStarted
	resetCIVisibilityStateForTesting()
	close(uploadRelease)
	<-done

	require.NoError(t, err)
	assert.Equal(t, int64(42), bytes)
	assert.Equal(t, 2, getSearchCalls)
}

func TestEnsureSettingsInitializationNilClientFactoryDoesNotStartUpload(t *testing.T) {
	resetCIVisibilityStateForTesting()
	t.Cleanup(resetCIVisibilityStateForTesting)

	newCIVisibilityClientWithServiceNameFunc = func(_ string) civisibilitynet.Client {
		return nil
	}
	uploadRepositoryChangesFunc = func() (int64, error) {
		t.Fatal("repository upload should not start without a CI Visibility client")
		return 0, nil
	}

	require.NotPanics(t, func() {
		ensureSettingsInitialization("service")
	})
	assert.Equal(t, civisibilitynet.SettingsResponseData{}, ciVisibilitySettings)
	assert.Len(t, closeActions, 0)
}

func TestEnsureSettingsInitializationHandlesNilInitialSettingsResponse(t *testing.T) {
	resetCIVisibilityStateForTesting()
	t.Cleanup(resetCIVisibilityStateForTesting)

	uploadStarted := make(chan struct{})
	uploadRelease := make(chan struct{})
	newCIVisibilityClientWithServiceNameFunc = func(_ string) civisibilitynet.Client {
		return &mockCIVisibilityClient{
			getSettings: func() (*civisibilitynet.SettingsResponseData, error) {
				return nil, nil
			},
		}
	}
	uploadRepositoryChangesFunc = func() (int64, error) {
		close(uploadStarted)
		<-uploadRelease
		return 0, nil
	}

	require.NotPanics(t, func() {
		ensureSettingsInitialization("service")
	})
	assert.Equal(t, civisibilitynet.SettingsResponseData{}, ciVisibilitySettings)
	assert.Len(t, closeActions, 1)

	close(uploadRelease)
	closeActions[0]()
	<-uploadStarted
}

func TestEnsureSettingsInitializationHandlesNilRetrySettingsResponse(t *testing.T) {
	resetCIVisibilityStateForTesting()
	t.Cleanup(resetCIVisibilityStateForTesting)

	settingsCalls := 0
	newCIVisibilityClientWithServiceNameFunc = func(_ string) civisibilitynet.Client {
		return &mockCIVisibilityClient{
			getSettings: func() (*civisibilitynet.SettingsResponseData, error) {
				settingsCalls++
				if settingsCalls == 1 {
					return &civisibilitynet.SettingsResponseData{RequireGit: true}, nil
				}
				return nil, nil
			},
		}
	}
	uploadRepositoryChangesFunc = func() (int64, error) {
		return 0, nil
	}

	require.NotPanics(t, func() {
		ensureSettingsInitialization("service")
	})
	assert.Equal(t, 2, settingsCalls)
	assert.Equal(t, civisibilitynet.SettingsResponseData{}, ciVisibilitySettings)
	assert.Len(t, closeActions, 0)
}

// mockCIVisibilityClient implements net.Client for settings bootstrap tests.
type mockCIVisibilityClient struct {
	getSettings func() (*civisibilitynet.SettingsResponseData, error)
}

var _ civisibilitynet.Client = (*mockCIVisibilityClient)(nil)

func (m *mockCIVisibilityClient) GetSettings() (*civisibilitynet.SettingsResponseData, error) {
	if m.getSettings != nil {
		return m.getSettings()
	}
	return &civisibilitynet.SettingsResponseData{}, nil
}

func (m *mockCIVisibilityClient) GetKnownTests() (*civisibilitynet.KnownTestsResponseData, error) {
	return nil, nil
}

func (m *mockCIVisibilityClient) GetCommits(_ []string) ([]string, error) {
	return nil, nil
}

func (m *mockCIVisibilityClient) SendPackFiles(_ string, _ []string) (int64, error) {
	return 0, nil
}

func (m *mockCIVisibilityClient) SendCoveragePayload(_ io.Reader) error {
	return nil
}

func (m *mockCIVisibilityClient) SendCoveragePayloadWithFormat(_ io.Reader, _ string) error {
	return nil
}

func (m *mockCIVisibilityClient) GetSkippableTests() (string, map[string]map[string][]civisibilitynet.SkippableResponseDataAttributes, error) {
	return "", nil, nil
}

func (m *mockCIVisibilityClient) GetTestManagementTests() (*civisibilitynet.TestManagementTestsResponseDataModules, error) {
	return nil, nil
}

func (m *mockCIVisibilityClient) SendLogs(_ io.Reader) error {
	return nil
}

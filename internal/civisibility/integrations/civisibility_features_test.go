// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package integrations

import (
	"io"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	civisibilitynet "github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
	coretelemetry "github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

func TestAdditionalFeaturesInitializationPublishesLockFreeFastPath(t *testing.T) {
	resetCIVisibilityStateForTesting()
	t.Cleanup(resetCIVisibilityStateForTesting)

	additionalFeaturesInitializationOnce = sync.Once{}
	additionalFeaturesInitializationOnce.Do(func() {})
	require.False(t, additionalFeaturesInitialized.Load())

	ensureAdditionalFeaturesInitialization("")
	require.True(t, additionalFeaturesInitialized.Load())

	ensureAdditionalFeaturesInitialization("")
	require.True(t, additionalFeaturesInitialized.Load())
}

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

func TestEnsureSettingsInitializationGitUploadDisabledDoesNotStartUploadOrRetrySettings(t *testing.T) {
	resetCIVisibilityStateForTesting()
	t.Cleanup(resetCIVisibilityStateForTesting)
	t.Setenv(constants.CIVisibilityGitUploadEnabledEnvironmentVariable, "false")

	settingsCalls := 0
	newCIVisibilityClientWithServiceNameFunc = func(_ string) civisibilitynet.Client {
		return &mockCIVisibilityClient{
			getSettings: func() (*civisibilitynet.SettingsResponseData, error) {
				settingsCalls++
				return &civisibilitynet.SettingsResponseData{RequireGit: true}, nil
			},
		}
	}
	uploadRepositoryChangesFunc = func() (int64, error) {
		t.Fatal("repository upload should not start when git upload is disabled")
		return 0, nil
	}

	require.NotPanics(t, func() {
		ensureSettingsInitialization("service")
	})
	assert.Equal(t, 1, settingsCalls)
	assert.True(t, ciVisibilitySettings.RequireGit)
	assert.Len(t, closeActions, 0)
}

func TestEnsureSettingsInitializationFlakyRetryEnvironmentOverrideEnablesRetries(t *testing.T) {
	resetCIVisibilityStateForTesting()
	t.Cleanup(resetCIVisibilityStateForTesting)
	t.Setenv(constants.CIVisibilityGitUploadEnabledEnvironmentVariable, "false")
	t.Setenv(constants.CIVisibilityFlakyRetryEnabledEnvironmentVariable, "true")
	t.Setenv(constants.CIVisibilityFlakyRetryCountEnvironmentVariable, "2")
	t.Setenv(constants.CIVisibilityTotalFlakyRetryCountEnvironmentVariable, "5")

	newCIVisibilityClientWithServiceNameFunc = func(_ string) civisibilitynet.Client {
		return &mockCIVisibilityClient{
			getSettings: func() (*civisibilitynet.SettingsResponseData, error) {
				return &civisibilitynet.SettingsResponseData{FlakyTestRetriesEnabled: false}, nil
			},
		}
	}
	uploadRepositoryChangesFunc = func() (int64, error) {
		t.Fatal("repository upload should not start when git upload is disabled")
		return 0, nil
	}

	ensureSettingsInitialization("service")
	ensureAdditionalFeaturesInitialization("service")

	assert.True(t, ciVisibilitySettings.FlakyTestRetriesEnabled)
	assert.Equal(t, FlakyRetriesSetting{
		RetryCount:               2,
		TotalRetryCount:          5,
		RemainingTotalRetryCount: 5,
	}, ciVisibilityFlakyRetriesSettings)
}

func TestEnsureAdditionalFeaturesInitializationDynamicFlakyRetryCountsIgnoreEFDCap(t *testing.T) {
	// The environment is set before the reset so that the CI Visibility initializer started by TestMain, if it runs
	// after the reset, builds the same state as this test.
	t.Setenv(constants.CIVisibilityGitUploadEnabledEnvironmentVariable, "false")
	t.Setenv(constants.CIVisibilityDynamicFlakyRetryEnabledEnvironmentVariable, "true")
	t.Setenv(constants.CIVisibilityEarlyFlakeDetectionMaxRetriesEnvironmentVariable, "1")
	resetCIVisibilityStateForTesting()
	t.Cleanup(resetCIVisibilityStateForTesting)
	newCIVisibilityClientWithServiceNameFunc = func(_ string) civisibilitynet.Client {
		return &mockCIVisibilityClient{
			getSettings: func() (*civisibilitynet.SettingsResponseData, error) {
				settings := &civisibilitynet.SettingsResponseData{FlakyTestRetriesEnabled: true}
				settings.EarlyFlakeDetection.SlowTestRetries.FiveS = 4
				settings.EarlyFlakeDetection.SlowTestRetries.ThirtyS = 2
				return settings, nil
			},
		}
	}

	ensureSettingsInitialization("service")
	ensureAdditionalFeaturesInitialization("service")

	assert.Equal(t, 1, ciVisibilitySettings.EarlyFlakeDetection.SlowTestRetries.FiveS)
	assert.Equal(t, &[5]int64{4, 5, 2, 2, 1}, ciVisibilityFlakyRetriesSettings.DynamicRetryCounts)
}

func TestDynamicFlakyRetryCounts(t *testing.T) {
	enabledMetric := telemetrytest.MetricKey{Namespace: coretelemetry.NamespaceCIVisibility, Name: "dynamic_atr_retries.enabled", Kind: "count"}
	customBucketsMetric := enabledMetric
	customBucketsMetric.Tags = "has_custom_buckets:true"

	tests := []struct {
		name       string
		enabled    string
		buckets    string
		wantCounts *[5]int64
		wantMetric *telemetrytest.MetricKey
	}{
		{name: "disabled ignores buckets", buckets: "3,2,1,1,1"},
		{name: "slow test retries", enabled: "true", wantCounts: &[5]int64{4, 5, 2, 2, 1}, wantMetric: &enabledMetric},
		{name: "custom buckets", enabled: "1", buckets: " 3, 2,1,1 ,20", wantCounts: &[5]int64{3, 2, 1, 1, 20}, wantMetric: &customBucketsMetric},
		{name: "invalid buckets", enabled: "true", buckets: "3,2,1,1,21", wantCounts: &[5]int64{4, 5, 2, 2, 1}, wantMetric: &enabledMetric},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := new(telemetrytest.RecordClient)
			defer coretelemetry.MockClient(recorder)()
			t.Setenv(constants.CIVisibilityDynamicFlakyRetryEnabledEnvironmentVariable, tt.enabled)
			t.Setenv(constants.CIVisibilityDynamicFlakyRetryBucketsEnvironmentVariable, tt.buckets)

			assert.Equal(t, tt.wantCounts, dynamicFlakyRetryCounts([4]int{4, 0, 2, 0}))
			if tt.wantMetric == nil {
				assert.NotContains(t, recorder.Metrics, enabledMetric)
				assert.NotContains(t, recorder.Metrics, customBucketsMetric)
				return
			}
			require.Contains(t, recorder.Metrics, *tt.wantMetric)
			assert.Equal(t, 1.0, recorder.Metrics[*tt.wantMetric].Get())
		})
	}
}

func TestDynamicFlakyRetryCountsWithoutSlowTestRetries(t *testing.T) {
	defer coretelemetry.MockClient(new(telemetrytest.RecordClient))()
	t.Setenv(constants.CIVisibilityDynamicFlakyRetryEnabledEnvironmentVariable, "true")
	t.Setenv(constants.CIVisibilityDynamicFlakyRetryBucketsEnvironmentVariable, "")

	assert.Equal(t, &[5]int64{10, 5, 3, 2, 1}, dynamicFlakyRetryCounts([4]int{}))
}

func TestParseDynamicFlakyRetryCounts(t *testing.T) {
	counts, ok := parseDynamicFlakyRetryCounts("6,4,3,1,1")
	assert.True(t, ok)
	assert.Equal(t, [5]int64{6, 4, 3, 1, 1}, counts)

	for _, value := range []string{"", "3,2,1,1", "3,2,1,1,1,1", "3,2,1,1,1,", "3,2,,1,1", "3,2,a,1,1", "3,2,0,1,1", "3,2,-1,1,1", "3,2,21,1,1"} {
		_, ok := parseDynamicFlakyRetryCounts(value)
		assert.False(t, ok, "value %q", value)
	}
}

func TestEnsureSettingsInitializationGitUploadDisabledSettingsErrorDoesNotRegisterCloseAction(t *testing.T) {
	resetCIVisibilityStateForTesting()
	t.Cleanup(resetCIVisibilityStateForTesting)
	t.Setenv(constants.CIVisibilityGitUploadEnabledEnvironmentVariable, "false")

	newCIVisibilityClientWithServiceNameFunc = func(_ string) civisibilitynet.Client {
		return &mockCIVisibilityClient{
			getSettings: func() (*civisibilitynet.SettingsResponseData, error) {
				return nil, nil
			},
		}
	}
	uploadRepositoryChangesFunc = func() (int64, error) {
		t.Fatal("repository upload should not start when git upload is disabled")
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

func (m *mockCIVisibilityClient) SendCoverageReport(_ io.Reader, _ string) error {
	return nil
}

func (m *mockCIVisibilityClient) GetSkippableTests() (*civisibilitynet.SkippableTestsResponse, error) {
	return nil, nil
}

func (m *mockCIVisibilityClient) GetTestManagementTests() (*civisibilitynet.TestManagementTestsResponseDataModules, error) {
	return nil, nil
}

func (m *mockCIVisibilityClient) SendLogs(_ io.Reader) error {
	return nil
}

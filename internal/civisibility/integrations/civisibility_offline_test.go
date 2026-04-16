// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package integrations

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/internal/bazel"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

func TestEnsureSettingsInitializationManifestModeSkipsRepositoryUpload(t *testing.T) {
	resetCIVisibilityStateForTesting()
	t.Cleanup(resetCIVisibilityStateForTesting)

	t.Setenv(bazel.ManifestFilePathEnv, writeSettingsManifestCache(t, true, true, true))
	bazel.ResetForTesting()

	var uploadCalls int
	uploadRepositoryChangesFunc = func() (int64, error) {
		uploadCalls++
		return 0, nil
	}

	ensureSettingsInitialization("manifest-service")

	assert.Equal(t, 0, uploadCalls)
	assert.Len(t, closeActions, 0)
	assert.True(t, ciVisibilitySettings.RequireGit)
	assert.False(t, ciVisibilitySettings.TestsSkipping)
}

func TestEnsureSettingsInitializationPayloadFilesModeSkipsRepositoryUploadAndDisablesImpactedTests(t *testing.T) {
	resetCIVisibilityStateForTesting()
	t.Cleanup(resetCIVisibilityStateForTesting)

	t.Setenv(bazel.ManifestFilePathEnv, writeSettingsManifestCache(t, true, true, true))
	t.Setenv(bazel.PayloadsInFilesEnv, "true")
	t.Setenv(bazel.UndeclaredOutputsDirEnv, t.TempDir())
	bazel.ResetForTesting()

	var uploadCalls int
	uploadRepositoryChangesFunc = func() (int64, error) {
		uploadCalls++
		return 0, nil
	}

	ensureSettingsInitialization("payload-files-service")

	assert.Equal(t, 0, uploadCalls)
	assert.Len(t, closeActions, 0)
	assert.True(t, ciVisibilitySettings.RequireGit)
	assert.False(t, ciVisibilitySettings.ImpactedTestsEnabled)
}

func TestEnsureSettingsInitializationManifestModeAppliesSubtestFeaturesEnvOverride(t *testing.T) {
	resetCIVisibilityStateForTesting()
	t.Cleanup(resetCIVisibilityStateForTesting)

	t.Setenv(bazel.ManifestFilePathEnv, writeSettingsManifestCache(t, true, false, false))
	t.Setenv(constants.CIVisibilitySubtestFeaturesEnabled, "false")
	bazel.ResetForTesting()

	ensureSettingsInitialization("manifest-service")

	assert.True(t, ciVisibilitySettings.RequireGit)
	assert.False(t, ciVisibilitySettings.SubtestFeaturesEnabled)
	assert.Len(t, closeActions, 0)
}

func TestEnsureSettingsInitializationOnlineSettingsErrorRegistersCloseAction(t *testing.T) {
	resetCIVisibilityStateForTesting()
	t.Cleanup(resetCIVisibilityStateForTesting)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "temporary backend error", http.StatusInternalServerError)
	}))
	defer server.Close()

	t.Setenv(constants.CIVisibilityAgentlessEnabledEnvironmentVariable, "true")
	t.Setenv(constants.APIKeyEnvironmentVariable, "test_api_key")
	t.Setenv(constants.CIVisibilityAgentlessURLEnvironmentVariable, server.URL)
	t.Setenv("DD_GIT_REPOSITORY_URL", "https://github.com/DataDog/dd-trace-go.git")
	t.Setenv("DD_GIT_COMMIT_SHA", "1234567890abcdef1234567890abcdef12345678")
	t.Setenv("DD_GIT_BRANCH", "refs/heads/main")

	uploadDone := make(chan struct{}, 1)
	uploadRepositoryChangesFunc = func() (int64, error) {
		uploadDone <- struct{}{}
		return 0, nil
	}

	ensureSettingsInitialization("online-service")

	select {
	case <-uploadDone:
	case <-time.After(time.Second):
		t.Fatal("expected repository upload to start")
	}

	assert.Len(t, closeActions, 1)
	assert.False(t, ciVisibilitySettings.RequireGit)
}

func TestShouldInitializeCiVisibilityLogsDisablesManifestMode(t *testing.T) {
	t.Setenv(bazel.ManifestFilePathEnv, writeSettingsManifestCache(t, false, false, false))
	bazel.ResetForTesting()
	t.Cleanup(bazel.ResetForTesting)

	assert.False(t, shouldInitializeCiVisibilityLogs(false))
	assert.False(t, shouldInitializeCiVisibilityLogs(true))
}

func TestShouldInitializeCiVisibilityLogsDisablesPayloadFilesMode(t *testing.T) {
	t.Setenv(bazel.PayloadsInFilesEnv, "true")
	t.Setenv(bazel.UndeclaredOutputsDirEnv, t.TempDir())
	bazel.ResetForTesting()
	t.Cleanup(bazel.ResetForTesting)

	assert.False(t, shouldInitializeCiVisibilityLogs(false))
	assert.False(t, shouldInitializeCiVisibilityLogs(true))
}

func TestShouldInitializeCiVisibilityLogsAllowsOnlineEnabledMode(t *testing.T) {
	bazel.ResetForTesting()
	t.Cleanup(bazel.ResetForTesting)

	assert.False(t, shouldInitializeCiVisibilityLogs(false))
	assert.True(t, shouldInitializeCiVisibilityLogs(true))
}

func TestInitializeCiVisibilityLogsSkipsOfflineModes(t *testing.T) {
	recordLogger := new(log.RecordLogger)
	oldLevel := log.GetLevel()
	defer log.UseLogger(recordLogger)()
	log.SetLevel(log.LevelDebug)
	defer log.SetLevel(oldLevel)

	t.Run("manifest", func(t *testing.T) {
		t.Setenv(bazel.ManifestFilePathEnv, writeSettingsManifestCache(t, false, false, false))
		bazel.ResetForTesting()
		t.Cleanup(bazel.ResetForTesting)

		initializeCiVisibilityLogs("manifest-service")

		assert.True(t, containsOfflineLogLine(recordLogger.Logs(), "logs initialization skipped for test optimization offline/file mode"))
	})

	t.Run("payload-files", func(t *testing.T) {
		t.Setenv(bazel.PayloadsInFilesEnv, "true")
		t.Setenv(bazel.UndeclaredOutputsDirEnv, t.TempDir())
		bazel.ResetForTesting()
		t.Cleanup(bazel.ResetForTesting)

		initializeCiVisibilityLogs("payload-files-service")

		assert.True(t, containsOfflineLogLine(recordLogger.Logs(), "logs initialization skipped for test optimization offline/file mode"))
	})
}

func TestInitializeCiVisibilityLogsReportsDisabledState(t *testing.T) {
	recordLogger := new(log.RecordLogger)
	oldLevel := log.GetLevel()
	defer log.UseLogger(recordLogger)()
	log.SetLevel(log.LevelDebug)
	defer log.SetLevel(oldLevel)

	bazel.ResetForTesting()
	t.Cleanup(bazel.ResetForTesting)

	initializeCiVisibilityLogs("online-service")

	assert.True(t, containsOfflineLogLine(recordLogger.Logs(), "logs are disabled"))
}

func TestEnsureSettingsInitializationAppliesEnvironmentOverrides(t *testing.T) {
	resetCIVisibilityStateForTesting()
	t.Cleanup(resetCIVisibilityStateForTesting)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"id":   "settings-id",
				"type": "ci_app_test_service_libraries_settings",
				"attributes": map[string]any{
					"require_git":                false,
					"flaky_test_retries_enabled": true,
					"impacted_tests_enabled":     true,
					"known_tests_enabled":        false,
					"tests_skipping":             false,
					"test_management": map[string]any{
						"enabled":                true,
						"attempt_to_fix_retries": 2,
					},
					"early_flake_detection": map[string]any{
						"enabled": true,
					},
				},
			},
		}))
	}))
	defer server.Close()

	t.Setenv(constants.CIVisibilityAgentlessEnabledEnvironmentVariable, "true")
	t.Setenv(constants.APIKeyEnvironmentVariable, "test_api_key")
	t.Setenv(constants.CIVisibilityAgentlessURLEnvironmentVariable, server.URL)
	t.Setenv("DD_GIT_REPOSITORY_URL", "https://github.com/DataDog/dd-trace-go.git")
	t.Setenv("DD_GIT_COMMIT_SHA", "1234567890abcdef1234567890abcdef12345678")
	t.Setenv("DD_GIT_BRANCH", "refs/heads/main")
	t.Setenv(constants.CIVisibilityFlakyRetryEnabledEnvironmentVariable, "false")
	t.Setenv(constants.CIVisibilityImpactedTestsDetectionEnabled, "false")
	t.Setenv(constants.CIVisibilityTestManagementEnabledEnvironmentVariable, "false")
	t.Setenv(constants.CIVisibilityTestManagementAttemptToFixRetriesEnvironmentVariable, "7")
	t.Setenv(constants.CIVisibilitySubtestFeaturesEnabled, "false")
	utils.ResetCITags()

	uploadDone := make(chan struct{}, 1)
	uploadRepositoryChangesFunc = func() (int64, error) {
		uploadDone <- struct{}{}
		return 0, nil
	}

	ensureSettingsInitialization("online-service")

	select {
	case <-uploadDone:
	case <-time.After(time.Second):
		t.Fatal("expected repository upload to start")
	}

	assert.False(t, ciVisibilitySettings.FlakyTestRetriesEnabled)
	assert.False(t, ciVisibilitySettings.ImpactedTestsEnabled)
	assert.False(t, ciVisibilitySettings.TestManagement.Enabled)
	assert.False(t, ciVisibilitySettings.EarlyFlakeDetection.Enabled)
	assert.Equal(t, 7, ciVisibilitySettings.TestManagement.AttemptToFixRetries)
	assert.False(t, ciVisibilitySettings.SubtestFeaturesEnabled)
	assert.Len(t, closeActions, 1)
}

func writeSettingsManifestCache(t *testing.T, requireGit bool, impactedTestsEnabled bool, testsSkipping bool) string {
	t.Helper()

	cacheDir := filepath.Join(t.TempDir(), ".testoptimization")
	manifestPath := filepath.Join(cacheDir, "manifest.txt")
	if err := os.MkdirAll(filepath.Join(cacheDir, "cache", "http"), 0o755); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}
	if err := os.WriteFile(manifestPath, []byte("1\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	payload := map[string]any{
		"data": map[string]any{
			"id":   "settings-id",
			"type": "ci_app_test_service_libraries_settings",
			"attributes": map[string]any{
				"require_git":            requireGit,
				"impacted_tests_enabled": impactedTestsEnabled,
				"itr_enabled":            testsSkipping,
				"tests_skipping":         testsSkipping,
			},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal settings payload: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "cache", "http", "settings.json"), raw, 0o644); err != nil {
		t.Fatalf("write settings cache: %v", err)
	}

	return manifestPath
}

func containsOfflineLogLine(lines []string, want string) bool {
	for _, line := range lines {
		if strings.Contains(line, want) {
			return true
		}
	}
	return false
}

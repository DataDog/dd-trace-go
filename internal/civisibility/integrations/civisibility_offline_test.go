// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package integrations

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
)

func TestEnsureSettingsInitializationManifestModeSkipsRepositoryUpload(t *testing.T) {
	resetCIVisibilityStateForTesting()
	t.Cleanup(resetCIVisibilityStateForTesting)

	t.Setenv(constants.CIVisibilityManifestFilePath, writeSettingsManifestCache(t, true, true, true))
	utils.ResetTestOptimizationModeForTesting()

	var uploadCalls int
	uploadRepositoryChangesFunc = func() (int64, error) {
		uploadCalls++
		return 0, nil
	}

	ensureSettingsInitialization("manifest-service")

	assert.Equal(t, 0, uploadCalls)
	assert.True(t, ciVisibilitySettings.RequireGit)
	assert.False(t, ciVisibilitySettings.TestsSkipping)
}

func TestEnsureSettingsInitializationPayloadFilesModeSkipsRepositoryUploadAndDisablesImpactedTests(t *testing.T) {
	resetCIVisibilityStateForTesting()
	t.Cleanup(resetCIVisibilityStateForTesting)

	t.Setenv(constants.CIVisibilityManifestFilePath, writeSettingsManifestCache(t, true, true, true))
	t.Setenv(constants.CIVisibilityPayloadsInFiles, "true")
	t.Setenv(constants.CIVisibilityUndeclaredOutputsDir, t.TempDir())
	utils.ResetTestOptimizationModeForTesting()

	var uploadCalls int
	uploadRepositoryChangesFunc = func() (int64, error) {
		uploadCalls++
		return 0, nil
	}

	ensureSettingsInitialization("payload-files-service")

	assert.Equal(t, 0, uploadCalls)
	assert.True(t, ciVisibilitySettings.RequireGit)
	assert.False(t, ciVisibilitySettings.ImpactedTestsEnabled)
}

func TestShouldInitializeCiVisibilityLogsDisablesManifestMode(t *testing.T) {
	t.Setenv(constants.CIVisibilityManifestFilePath, writeSettingsManifestCache(t, false, false, false))
	utils.ResetTestOptimizationModeForTesting()
	t.Cleanup(utils.ResetTestOptimizationModeForTesting)

	assert.False(t, shouldInitializeCiVisibilityLogs(false))
	assert.False(t, shouldInitializeCiVisibilityLogs(true))
}

func TestShouldInitializeCiVisibilityLogsDisablesPayloadFilesMode(t *testing.T) {
	t.Setenv(constants.CIVisibilityPayloadsInFiles, "true")
	t.Setenv(constants.CIVisibilityUndeclaredOutputsDir, t.TempDir())
	utils.ResetTestOptimizationModeForTesting()
	t.Cleanup(utils.ResetTestOptimizationModeForTesting)

	assert.False(t, shouldInitializeCiVisibilityLogs(false))
	assert.False(t, shouldInitializeCiVisibilityLogs(true))
}

func TestShouldInitializeCiVisibilityLogsAllowsOnlineEnabledMode(t *testing.T) {
	utils.ResetTestOptimizationModeForTesting()
	t.Cleanup(utils.ResetTestOptimizationModeForTesting)

	assert.False(t, shouldInitializeCiVisibilityLogs(false))
	assert.True(t, shouldInitializeCiVisibilityLogs(true))
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

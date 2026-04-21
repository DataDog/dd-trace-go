// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package net

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/internal/bazel"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

func TestSettingsApiRequest(t *testing.T) {
	var c *client
	expectedResponse := settingsResponse{}
	expectedResponse.Data.Type = settingsRequestType
	expectedResponse.Data.Attributes.FlakyTestRetriesEnabled = true
	expectedResponse.Data.Attributes.CodeCoverage = true
	expectedResponse.Data.Attributes.TestsSkipping = true
	expectedResponse.Data.Attributes.ItrEnabled = true
	expectedResponse.Data.Attributes.RequireGit = true
	expectedResponse.Data.Attributes.EarlyFlakeDetection.FaultySessionThreshold = 30
	expectedResponse.Data.Attributes.EarlyFlakeDetection.Enabled = true
	expectedResponse.Data.Attributes.EarlyFlakeDetection.SlowTestRetries.FiveS = 25
	expectedResponse.Data.Attributes.EarlyFlakeDetection.SlowTestRetries.TenS = 20
	expectedResponse.Data.Attributes.EarlyFlakeDetection.SlowTestRetries.ThirtyS = 10
	expectedResponse.Data.Attributes.EarlyFlakeDetection.SlowTestRetries.FiveM = 5

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}

		if r.Header.Get(HeaderContentType) == ContentTypeJSON {
			var request settingsRequest
			json.Unmarshal(body, &request)
			assert.Equal(t, c.id, request.Data.ID)
			assert.Equal(t, settingsRequestType, request.Data.Type)
			assert.Equal(t, settingsURLPath, r.URL.Path[1:])
			assert.Equal(t, c.commitSha, request.Data.Attributes.Sha)
			assert.Equal(t, c.branchName, request.Data.Attributes.Branch)
			assert.Equal(t, c.environment, request.Data.Attributes.Env)
			assert.Equal(t, c.repositoryURL, request.Data.Attributes.RepositoryURL)
			assert.Equal(t, c.serviceName, request.Data.Attributes.Service)
			assert.Equal(t, c.testConfigurations, request.Data.Attributes.Configurations)

			w.Header().Set(HeaderContentType, ContentTypeJSON)
			expectedResponse.Data.ID = request.Data.ID
			json.NewEncoder(w).Encode(expectedResponse)
		}
	}))
	defer server.Close()

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, server.URL)

	cInterface := NewClient()
	c = cInterface.(*client)
	settings, err := cInterface.GetSettings()
	assert.Nil(t, err)
	assert.Equal(t, expectedResponse.Data.Attributes, *settings)
}

func TestSettingsApiRequestFailToUnmarshal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "failed to read body", http.StatusBadRequest)
	}))
	defer server.Close()

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, server.URL)

	cInterface := NewClient()
	settings, err := cInterface.GetSettings()
	assert.Nil(t, settings)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "cannot unmarshal response")
}

func TestSettingsApiRequestFailToGet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal processing error", http.StatusInternalServerError)
	}))
	defer server.Close()

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, server.URL)

	cInterface := NewClient()
	settings, err := cInterface.GetSettings()
	assert.Nil(t, settings)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "sending get settings request")
}

func TestSettingsApiRequestFromManifestCache(t *testing.T) {
	bazel.ResetForTesting()
	t.Cleanup(bazel.ResetForTesting)

	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		http.Error(w, "unexpected network call", http.StatusInternalServerError)
	}))
	defer server.Close()

	expectedResponse := settingsResponse{}
	expectedResponse.Data.Attributes.FlakyTestRetriesEnabled = true
	expectedResponse.Data.Attributes.CodeCoverage = true
	expectedResponse.Data.Attributes.TestsSkipping = true
	expectedResponse.Data.Attributes.ItrEnabled = true
	expectedResponse.Data.Attributes.KnownTestsEnabled = true
	expectedResponse.Data.Attributes.ImpactedTestsEnabled = true
	expectedResponse.Data.Attributes.TestManagement.Enabled = true

	cacheDir := filepath.Join(t.TempDir(), ".testoptimization")
	manifestPath := filepath.Join(cacheDir, "manifest.txt")
	if err := os.MkdirAll(filepath.Join(cacheDir, "cache", "http"), 0o755); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}
	if err := os.WriteFile(manifestPath, []byte("1\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	rawResponse, err := json.Marshal(expectedResponse)
	if err != nil {
		t.Fatalf("marshal cache response: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "cache", "http", "settings.json"), rawResponse, 0o644); err != nil {
		t.Fatalf("write settings cache: %v", err)
	}

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)
	setCiVisibilityEnv(path, server.URL)
	os.Setenv(bazel.ManifestFilePathEnv, manifestPath)

	recordLogger := new(log.RecordLogger)
	oldLevel := log.GetLevel()
	defer log.UseLogger(recordLogger)()
	log.SetLevel(log.LevelDebug)
	defer log.SetLevel(oldLevel)

	cInterface := NewClient()
	settings, err := cInterface.GetSettings()
	assert.NoError(t, err)
	assert.Equal(t, expectedResponse.Data.Attributes, *settings)
	assert.Equal(t, 0, hits)
	assert.True(t, containsLogLine(recordLogger.Logs(), "reading .testoptimization/cache/http/settings.json"))
	assert.True(t, containsLogLine(recordLogger.Logs(), "loaded settings from .testoptimization/cache/http/settings.json"))
	assert.True(t, containsLogLine(recordLogger.Logs(), "enabled features [code_coverage:true itr:true tests_skipping:true known_tests:true impacted_tests:true early_flake_detection:false flaky_test_retries:true test_management:true require_git:false attempt_to_fix_retries:0]"))
}

func TestSettingsApiRequestFromManifestCacheMissingFile(t *testing.T) {
	bazel.ResetForTesting()
	t.Cleanup(bazel.ResetForTesting)

	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		http.Error(w, "unexpected network call", http.StatusInternalServerError)
	}))
	defer server.Close()

	cacheDir := filepath.Join(t.TempDir(), ".testoptimization")
	manifestPath := filepath.Join(cacheDir, "manifest.txt")
	if err := os.MkdirAll(filepath.Join(cacheDir, "cache", "http"), 0o755); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}
	if err := os.WriteFile(manifestPath, []byte("1\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)
	setCiVisibilityEnv(path, server.URL)
	os.Setenv(bazel.ManifestFilePathEnv, manifestPath)

	cInterface := NewClient()
	settings, err := cInterface.GetSettings()
	assert.NoError(t, err)
	assert.Equal(t, SettingsResponseData{}, *settings)
	assert.Equal(t, 0, hits)
}

func TestSettingsApiRequestFromManifestCacheMalformedFile(t *testing.T) {
	bazel.ResetForTesting()
	t.Cleanup(bazel.ResetForTesting)

	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		http.Error(w, "unexpected network call", http.StatusInternalServerError)
	}))
	defer server.Close()

	cacheDir := filepath.Join(t.TempDir(), ".testoptimization")
	manifestPath := filepath.Join(cacheDir, "manifest.txt")
	if err := os.MkdirAll(filepath.Join(cacheDir, "cache", "http"), 0o755); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}
	if err := os.WriteFile(manifestPath, []byte("1\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "cache", "http", "settings.json"), []byte("{invalid"), 0o644); err != nil {
		t.Fatalf("write malformed settings cache: %v", err)
	}

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)
	setCiVisibilityEnv(path, server.URL)
	os.Setenv(bazel.ManifestFilePathEnv, manifestPath)

	recordLogger := new(log.RecordLogger)
	oldLevel := log.GetLevel()
	defer log.UseLogger(recordLogger)()
	log.SetLevel(log.LevelDebug)
	defer log.SetLevel(oldLevel)

	cInterface := NewClient()
	settings, err := cInterface.GetSettings()
	assert.NoError(t, err)
	assert.Equal(t, SettingsResponseData{}, *settings)
	assert.Equal(t, 0, hits)
	assert.True(t, containsLogLine(recordLogger.Logs(), "invalid settings file"))
	assert.True(t, containsLogLine(recordLogger.Logs(), "returning empty settings because manifest cache is unavailable or invalid"))
}

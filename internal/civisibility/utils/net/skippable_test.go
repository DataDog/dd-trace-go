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
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	civisibilityutils "github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
)

func TestSkippableApiRequest(t *testing.T) {
	var c *client
	expectedResponse := skippableResponse{
		Meta: skippableResponseMeta{
			CorrelationID: "correlation_id",
		},
		Data: []skippableResponseData{
			{
				ID:   "id",
				Type: "type",
				Attributes: SkippableResponseDataAttributes{
					Suite:      "suite",
					Name:       "name",
					Parameters: "",
				},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}

		if r.Header.Get(HeaderContentType) == ContentTypeJSON {
			var request skippableRequest
			json.Unmarshal(body, &request)
			assert.Equal(t, skippableRequestType, request.Data.Type)
			assert.Equal(t, "test", request.Data.Attributes.TestLevel)
			assert.Equal(t, c.environment, request.Data.Attributes.Env)
			assert.Equal(t, c.serviceName, request.Data.Attributes.Service)
			assert.Equal(t, c.repositoryURL, request.Data.Attributes.RepositoryURL)
			assert.Equal(t, c.commitSha, request.Data.Attributes.Sha)
			expectedResponse.Data[0].Attributes.Configurations = c.testConfigurations
			w.Header().Set(HeaderContentType, ContentTypeJSON)
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
	correlationID, skippables, err := cInterface.GetSkippableTests()
	assert.Nil(t, err)
	assert.Equal(t, "correlation_id", correlationID)
	assert.Len(t, skippables, 1)
	assert.Len(t, skippables["suite"], 1)
	assert.Equal(t, expectedResponse.Data[0].Attributes, skippables["suite"]["name"][0])
}

func TestSkippableApiRequestFailToUnmarshal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "failed to read body", http.StatusBadRequest)
	}))
	defer server.Close()

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, server.URL)

	cInterface := NewClient()
	correlationID, skippables, err := cInterface.GetSkippableTests()
	assert.Empty(t, correlationID)
	assert.Nil(t, skippables)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "cannot unmarshal response")
}

func TestSkippableApiRequestFailToGet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal processing error", http.StatusInternalServerError)
	}))
	defer server.Close()

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, server.URL)

	cInterface := NewClient()
	correlationID, skippables, err := cInterface.GetSkippableTests()
	assert.Empty(t, correlationID)
	assert.Nil(t, skippables)
	assert.Contains(t, err.Error(), "sending skippable tests request")
}

func TestSkippableApiRequestFromManifestCache(t *testing.T) {
	civisibilityutils.ResetTestOptimizationModeForTesting()
	t.Cleanup(civisibilityutils.ResetTestOptimizationModeForTesting)

	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		http.Error(w, "unexpected network call", http.StatusInternalServerError)
	}))
	defer server.Close()

	cachedResponse := skippableResponse{
		Meta: skippableResponseMeta{
			CorrelationID: "cache-correlation-id",
		},
		Data: []skippableResponseData{
			{
				ID:   "id-1",
				Type: "test",
				Attributes: SkippableResponseDataAttributes{
					Suite: "suite",
					Name:  "match",
					Configurations: testConfigurations{
						OsPlatform: runtime.GOOS,
					},
				},
			},
			{
				ID:   "id-2",
				Type: "test",
				Attributes: SkippableResponseDataAttributes{
					Suite: "suite",
					Name:  "filtered-out",
					Configurations: testConfigurations{
						OsPlatform: "definitely-not-a-real-os",
					},
				},
			},
		},
	}

	cacheDir := filepath.Join(t.TempDir(), ".testoptimization")
	manifestPath := filepath.Join(cacheDir, "manifest.txt")
	if err := os.MkdirAll(filepath.Join(cacheDir, "cache", "http"), 0o755); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}
	if err := os.WriteFile(manifestPath, []byte("1\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	rawResponse, err := json.Marshal(cachedResponse)
	if err != nil {
		t.Fatalf("marshal cache response: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "cache", "http", "skippable_tests.json"), rawResponse, 0o644); err != nil {
		t.Fatalf("write skippable cache: %v", err)
	}

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)
	setCiVisibilityEnv(path, server.URL)
	os.Setenv(constants.CIVisibilityManifestFilePath, manifestPath)

	cInterface := NewClient()
	correlationID, skippables, err := cInterface.GetSkippableTests()
	assert.NoError(t, err)
	assert.Equal(t, "cache-correlation-id", correlationID)
	assert.Equal(t, 0, hits)
	assert.Contains(t, skippables, "suite")
	assert.Contains(t, skippables["suite"], "match")
	assert.NotContains(t, skippables["suite"], "filtered-out")
}

func TestSkippableApiRequestFromManifestCacheMissingFile(t *testing.T) {
	civisibilityutils.ResetTestOptimizationModeForTesting()
	t.Cleanup(civisibilityutils.ResetTestOptimizationModeForTesting)

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
	os.Setenv(constants.CIVisibilityManifestFilePath, manifestPath)

	cInterface := NewClient()
	correlationID, skippables, err := cInterface.GetSkippableTests()
	assert.NoError(t, err)
	assert.Equal(t, "", correlationID)
	assert.Equal(t, map[string]map[string][]SkippableResponseDataAttributes{}, skippables)
	assert.Equal(t, 0, hits)
}

func TestSkippableApiRequestFromManifestCacheMalformedFile(t *testing.T) {
	civisibilityutils.ResetTestOptimizationModeForTesting()
	t.Cleanup(civisibilityutils.ResetTestOptimizationModeForTesting)

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
	if err := os.WriteFile(filepath.Join(cacheDir, "cache", "http", "skippable_tests.json"), []byte("{invalid"), 0o644); err != nil {
		t.Fatalf("write malformed skippable cache: %v", err)
	}

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)
	setCiVisibilityEnv(path, server.URL)
	os.Setenv(constants.CIVisibilityManifestFilePath, manifestPath)

	cInterface := NewClient()
	correlationID, skippables, err := cInterface.GetSkippableTests()
	assert.NoError(t, err)
	assert.Equal(t, "", correlationID)
	assert.Equal(t, map[string]map[string][]SkippableResponseDataAttributes{}, skippables)
	assert.Equal(t, 0, hits)
}

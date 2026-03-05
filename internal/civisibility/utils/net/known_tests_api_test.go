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

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	civisibilityutils "github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
	"github.com/stretchr/testify/assert"
)

func TestKnownTestsApiRequest(t *testing.T) {
	var c *client
	expectedResponse := knownTestsResponse{}
	expectedResponse.Data.Type = settingsRequestType
	expectedResponse.Data.Attributes.Tests = KnownTestsResponseDataModules{
		"MyModule1": KnownTestsResponseDataSuites{
			"MySuite1": []string{"Test1", "Test2"},
		},
		"MyModule2": KnownTestsResponseDataSuites{
			"MySuite2": []string{"Test3", "Test4"},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}

		if r.Header.Get(HeaderContentType) == ContentTypeJSON {
			var request knownTestsRequest
			json.Unmarshal(body, &request)
			assert.Equal(t, c.id, request.Data.ID)
			assert.Equal(t, knownTestsRequestType, request.Data.Type)
			assert.Equal(t, knownTestsURLPath, r.URL.Path[1:])
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
	efdData, err := cInterface.GetKnownTests()
	assert.Nil(t, err)
	assert.Equal(t, expectedResponse.Data.Attributes, *efdData)
}

func TestKnownTestsApiRequestFailToUnmarshal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "failed to read body", http.StatusBadRequest)
	}))
	defer server.Close()

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, server.URL)

	cInterface := NewClient()
	efdData, err := cInterface.GetKnownTests()
	assert.Nil(t, efdData)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "cannot unmarshal response")
}

func TestKnownTestsApiRequestFailToGet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal processing error", http.StatusInternalServerError)
	}))
	defer server.Close()

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, server.URL)

	cInterface := NewClient()
	efdData, err := cInterface.GetKnownTests()
	assert.Nil(t, efdData)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "sending known tests request")
}

func TestKnownTestsApiRequestFromManifestCache(t *testing.T) {
	civisibilityutils.ResetTestOptimizationModeForTesting()
	t.Cleanup(civisibilityutils.ResetTestOptimizationModeForTesting)

	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		http.Error(w, "unexpected network call", http.StatusInternalServerError)
	}))
	defer server.Close()

	expectedResponse := knownTestsResponse{}
	expectedResponse.Data.Attributes.Tests = KnownTestsResponseDataModules{
		"moduleA": {
			"suiteA": {"test1", "test2"},
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
	rawResponse, err := json.Marshal(expectedResponse)
	if err != nil {
		t.Fatalf("marshal cache response: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "cache", "http", "known_tests.json"), rawResponse, 0o644); err != nil {
		t.Fatalf("write known tests cache: %v", err)
	}

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)
	setCiVisibilityEnv(path, server.URL)
	os.Setenv(constants.CIVisibilityManifestFilePath, manifestPath)

	cInterface := NewClient()
	responseData, err := cInterface.GetKnownTests()
	assert.NoError(t, err)
	assert.Equal(t, expectedResponse.Data.Attributes, *responseData)
	assert.Equal(t, 0, hits)
}

func TestKnownTestsApiRequestFromManifestCacheMissingFile(t *testing.T) {
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
	responseData, err := cInterface.GetKnownTests()
	assert.NoError(t, err)
	assert.Equal(t, KnownTestsResponseData{Tests: KnownTestsResponseDataModules{}}, *responseData)
	assert.Equal(t, 0, hits)
}

func TestKnownTestsApiRequestFromManifestCacheMalformedFile(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(cacheDir, "cache", "http", "known_tests.json"), []byte("{invalid"), 0o644); err != nil {
		t.Fatalf("write malformed known tests cache: %v", err)
	}

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)
	setCiVisibilityEnv(path, server.URL)
	os.Setenv(constants.CIVisibilityManifestFilePath, manifestPath)

	cInterface := NewClient()
	responseData, err := cInterface.GetKnownTests()
	assert.NoError(t, err)
	assert.Equal(t, KnownTestsResponseData{Tests: KnownTestsResponseDataModules{}}, *responseData)
	assert.Equal(t, 0, hits)
}

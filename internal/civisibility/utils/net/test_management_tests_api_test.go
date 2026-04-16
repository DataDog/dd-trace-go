// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package net

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/bazel"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// TestTestManagementTestsApiRequest tests the successful scenario for GetTestManagementTests.
func TestTestManagementTestsApiRequest(t *testing.T) {
	var c *client
	// Create an expected response following the structure defined in the package.
	expectedResponse := testManagementTestsResponse{}
	expectedResponse.Data.Type = testManagementTestsRequestType
	expectedResponse.Data.Attributes.Modules = map[string]TestManagementTestsResponseDataSuites{
		"MyModule": {
			Suites: map[string]TestManagementTestsResponseDataTests{
				"MySuite": {
					Tests: map[string]TestManagementTestsResponseDataTestProperties{
						"Test1": {
							Properties: TestManagementTestsResponseDataTestPropertiesAttributes{
								Quarantined:  false,
								Disabled:     false,
								AttemptToFix: true,
							},
						},
					},
				},
			},
		},
	}

	// Create a test server that simulates the endpoint behavior.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read the request body.
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}

		// Check that the request contains the expected Content-Type.
		if r.Header.Get(HeaderContentType) == ContentTypeJSON {
			var request testManagementTestsRequest
			err = json.Unmarshal(body, &request)
			assert.NoError(t, err, "failed to unmarshal request body")

			// Validate that the request payload has the expected values.
			assert.Equal(t, c.id, request.Data.ID, "ID mismatch")
			assert.Equal(t, testManagementTestsRequestType, request.Data.Type, "Type mismatch")
			assert.Equal(t, c.repositoryURL, request.Data.Attributes.RepositoryURL, "RepositoryURL mismatch")
			assert.Equal(t, c.branchName, request.Data.Attributes.Branch, "Branch mismatch")
			// Check the URL (remove the "/" prefix).
			assert.Equal(t, testManagementTestsURLPath, r.URL.Path[1:], "URL path mismatch")

			// Set the response header and encode the expected JSON response.
			w.Header().Set(HeaderContentType, ContentTypeJSON)
			// Set the ID in the response to match the request.
			expectedResponse.Data.ID = request.Data.ID
			_ = json.NewEncoder(w).Encode(expectedResponse)
		}
	}))
	defer server.Close()

	// Save the original environment variables and restore them at the end of the test.
	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	// Set the test server URL in the environment.
	setCiVisibilityEnv(path, server.URL)

	// We create the client (we assume that NewClient() already configures c.repositoryURL, c.id, etc.).
	cInterface := NewClient()
	c = cInterface.(*client)

	// Let's call the function we want to test.
	responseData, err := cInterface.GetTestManagementTests()
	require.NoError(t, err)

	// Let's compare the part of the response we are interested in.
	assert.Equal(t, expectedResponse.Data.Attributes, *responseData)
}

// TestTestManagementTestsApiRequestFailToUnmarshal simulates a failure in the unmarshal of the response.
func TestTestManagementTestsApiRequestFailToUnmarshal(t *testing.T) {
	// The server returns a malformed JSON to trigger an unmarshal error.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(HeaderContentType, ContentTypeJSON)
		_, _ = w.Write([]byte(`{"invalid": "json"`)) // JSON malformado
	}))
	defer server.Close()

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, server.URL)

	cInterface := NewClient()
	responseData, err := cInterface.GetTestManagementTests()
	assert.Nil(t, responseData)
	assert.NotNil(t, err)

	// We expect the error to contain the string defined in the message.
	assert.Contains(t, err.Error(), "unmarshalling test management tests response")
}

// TestTestManagementTestsApiRequestFailToGet simulates a failure in the call to the endpoint.
func TestTestManagementTestsApiRequestFailToGet(t *testing.T) {
	// The server responds with an HTTP 500 error.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal processing error", http.StatusInternalServerError)
	}))
	defer server.Close()

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, server.URL)

	cInterface := NewClient()
	responseData, err := cInterface.GetTestManagementTests()
	assert.Nil(t, responseData)
	assert.NotNil(t, err)

	// We expect the error to contain the string defined in the message.
	assert.Contains(t, err.Error(), "sending known tests request")
}

func TestTestManagementTestsApiRequestFromManifestCache(t *testing.T) {
	bazel.ResetForTesting()
	t.Cleanup(bazel.ResetForTesting)

	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		http.Error(w, "unexpected network call", http.StatusInternalServerError)
	}))
	defer server.Close()

	expectedResponse := testManagementTestsResponse{}
	expectedResponse.Data.Attributes.Modules = map[string]TestManagementTestsResponseDataSuites{
		"moduleA": {
			Suites: map[string]TestManagementTestsResponseDataTests{
				"suiteA": {
					Tests: map[string]TestManagementTestsResponseDataTestProperties{
						"testA": {
							Properties: TestManagementTestsResponseDataTestPropertiesAttributes{
								Quarantined:  true,
								Disabled:     false,
								AttemptToFix: true,
							},
						},
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
	rawResponse, err := json.Marshal(expectedResponse)
	if err != nil {
		t.Fatalf("marshal cache response: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "cache", "http", "test_management.json"), rawResponse, 0o644); err != nil {
		t.Fatalf("write test management cache: %v", err)
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
	responseData, err := cInterface.GetTestManagementTests()
	assert.NoError(t, err)
	assert.Equal(t, expectedResponse.Data.Attributes, *responseData)
	assert.Equal(t, 0, hits)
	assert.True(t, containsTestManagementLogLine(recordLogger.Logs(), "reading .testoptimization/cache/http/test_management.json"))
	assert.True(t, containsTestManagementLogLine(recordLogger.Logs(), "loaded test management tests from .testoptimization/cache/http/test_management.json [modules:1 suites:1 tests:1]"))
}

func TestTestManagementTestsApiRequestFromManifestCacheMissingFile(t *testing.T) {
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
	responseData, err := cInterface.GetTestManagementTests()
	assert.NoError(t, err)
	assert.Equal(t, TestManagementTestsResponseDataModules{
		Modules: map[string]TestManagementTestsResponseDataSuites{},
	}, *responseData)
	assert.Equal(t, 0, hits)
}

func TestTestManagementTestsApiRequestFromManifestCacheMalformedFile(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(cacheDir, "cache", "http", "test_management.json"), []byte("{invalid"), 0o644); err != nil {
		t.Fatalf("write malformed test management cache: %v", err)
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
	responseData, err := cInterface.GetTestManagementTests()
	assert.NoError(t, err)
	assert.Equal(t, TestManagementTestsResponseDataModules{
		Modules: map[string]TestManagementTestsResponseDataSuites{},
	}, *responseData)
	assert.Equal(t, 0, hits)
	assert.True(t, containsTestManagementLogLine(recordLogger.Logs(), "invalid test management file"))
	assert.True(t, containsTestManagementLogLine(recordLogger.Logs(), "returning empty test management response because manifest cache is unavailable or invalid"))
}

func containsTestManagementLogLine(lines []string, want string) bool {
	for _, line := range lines {
		if strings.Contains(line, want) {
			return true
		}
	}
	return false
}

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
)

func TestSearchCommitsApiRequest(t *testing.T) {
	var c *client
	expectedResponse := searchCommits{
		Data: []searchCommitsData{
			{
				ID:   "commit3",
				Type: searchCommitsType,
			},
			{
				ID:   "commit4",
				Type: searchCommitsType,
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
			var request searchCommits
			json.Unmarshal(body, &request)
			assert.Equal(t, c.repositoryURL, request.Meta.RepositoryURL)
			assert.Equal(t, "commit1", request.Data[0].ID)
			assert.Equal(t, searchCommitsType, request.Data[0].Type)
			assert.Equal(t, "commit2", request.Data[1].ID)
			assert.Equal(t, searchCommitsType, request.Data[1].Type)

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
	remoteCommits, err := cInterface.GetCommits([]string{"commit1", "commit2"})
	assert.Nil(t, err)
	assert.Equal(t, []string{"commit3", "commit4"}, remoteCommits)
}

func TestSearchCommitsApiRequestFailToUnmarshal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "failed to read body", http.StatusBadRequest)
	}))
	defer server.Close()

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, server.URL)

	cInterface := NewClient()
	remoteCommits, err := cInterface.GetCommits([]string{"commit1", "commit2"})
	assert.Nil(t, remoteCommits)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "cannot unmarshal response")
}

func TestSearchCommitsApiRequestFailToGet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal processing error", http.StatusInternalServerError)
	}))
	defer server.Close()

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, server.URL)

	cInterface := NewClient()
	remoteCommits, err := cInterface.GetCommits([]string{"commit1", "commit2"})
	assert.Nil(t, remoteCommits)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "sending search commits request")
}

func TestSearchCommitsApiRequestManifestModeNoop(t *testing.T) {
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
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
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
	remoteCommits, err := cInterface.GetCommits([]string{"commit1", "commit2"})
	assert.NoError(t, err)
	assert.Equal(t, []string{}, remoteCommits)
	assert.Equal(t, 0, hits)
}

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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestImpactedTestsApiRequest(t *testing.T) {
	var c *client
	expectedResponse := impactedTestsResponse{}
	expectedResponse.Data.Type = impactedTestsRequestType
	expectedResponse.Data.Attributes = ImpactedTestsDetectionResponse{
		BaseSha: "abcdef1234567890",
		Files:   []string{"File1", "File2"},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}

		if r.Header.Get(HeaderContentType) == ContentTypeJSON {
			var request impactedTestsRequest
			json.Unmarshal(body, &request)
			assert.Equal(t, c.id, request.Data.ID)
			assert.Equal(t, impactedTestsRequestType, request.Data.Type)
			assert.Equal(t, impactedTestsURLPath, r.URL.Path[1:])
			assert.Equal(t, c.environment, request.Data.Attributes.Environment)
			assert.Equal(t, c.repositoryURL, request.Data.Attributes.RepositoryURL)
			assert.Equal(t, c.commitSha, request.Data.Attributes.Sha)

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
	impactedData, err := cInterface.GetImpactedTests()
	assert.Nil(t, err)
	assert.Equal(t, expectedResponse.Data.Attributes, *impactedData)
}

func TestImpactedTestsApiRequestFailToUnmarshal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "failed to read body", http.StatusBadRequest)
	}))
	defer server.Close()

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, server.URL)

	cInterface := NewClient()
	impactedData, err := cInterface.GetImpactedTests()
	assert.Nil(t, impactedData)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "cannot unmarshal response")
}

func TestImpactedTestsApiRequestFailToGet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal processing error", http.StatusInternalServerError)
	}))
	defer server.Close()

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, server.URL)

	cInterface := NewClient()
	impactedData, err := cInterface.GetImpactedTests()
	assert.Nil(t, impactedData)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "sending impacted tests request")
}

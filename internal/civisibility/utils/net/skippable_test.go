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
	"testing"

	"github.com/stretchr/testify/assert"
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

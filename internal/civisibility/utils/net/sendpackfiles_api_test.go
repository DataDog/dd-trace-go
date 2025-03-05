// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package net

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSendPackFilesApiRequest(t *testing.T) {
	var c *client

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reader, err := r.MultipartReader()
		if err != nil {
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}

		containsPushedSha := false
		containsPackFile := false
		for {
			part, errPart := reader.NextPart()
			if errPart == io.EOF {
				break
			}
			partName := part.FormName()
			buf := new(bytes.Buffer)
			buf.ReadFrom(part)
			if partName == "pushedSha" {
				assert.Equal(t, ContentTypeJSON, part.Header.Get(HeaderContentType))
				var request pushedShaBody
				json.Unmarshal(buf.Bytes(), &request)
				assert.Equal(t, c.repositoryURL, request.Meta.RepositoryURL)
				assert.Equal(t, c.commitSha, request.Data.ID)
				assert.Equal(t, searchCommitsType, request.Data.Type)
				containsPushedSha = true
			} else if partName == "packfile" {
				assert.Equal(t, ContentTypeOctetStream, part.Header.Get(HeaderContentType))
				assert.NotZero(t, buf.Bytes())
				containsPackFile = true
			}
		}

		assert.True(t, containsPushedSha)
		assert.True(t, containsPackFile)
	}))
	defer server.Close()

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, server.URL)

	cInterface := NewClient()
	c = cInterface.(*client)
	_, err := cInterface.SendPackFiles(c.commitSha, []string{
		"sendpackfiles_api_test.go",
	})
	assert.Nil(t, err)
}

func TestSendPackFilesApiRequestFailToUnmarshal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "failed to read body", http.StatusBadRequest)
	}))
	defer server.Close()

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, server.URL)

	cInterface := NewClient()
	c := cInterface.(*client)
	_, err := cInterface.SendPackFiles(c.commitSha, []string{
		"sendpackfiles_api_test.go",
	})
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "unexpected response code")
}

func TestSendPackFilesApiRequestFailToGet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal processing error", http.StatusInternalServerError)
	}))
	defer server.Close()

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, server.URL)

	cInterface := NewClient()
	c := cInterface.(*client)
	bytes, err := cInterface.SendPackFiles(c.commitSha, []string{
		"sendpackfiles_api_test.go",
	})
	assert.Zero(t, bytes)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "failed to send packfile request")
}

func TestSendPackFilesApiRequestFileError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal processing error", http.StatusInternalServerError)
	}))
	defer server.Close()

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, server.URL)

	cInterface := NewClient()
	c := cInterface.(*client)
	bytes, err := cInterface.SendPackFiles(c.commitSha, []string{
		"unknown.file",
	})
	assert.Zero(t, bytes)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "failed to read pack file")
}

func TestSendPackFilesApiRequestNoFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal processing error", http.StatusInternalServerError)
	}))
	defer server.Close()

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, server.URL)

	cInterface := NewClient()
	bytes, err := cInterface.SendPackFiles("", nil)
	assert.Zero(t, bytes)
	assert.Nil(t, err)
}

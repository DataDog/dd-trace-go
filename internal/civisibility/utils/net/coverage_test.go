// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package net

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCoverageApiRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reader, err := r.MultipartReader()
		if err != nil {
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}

		containsDummyEvent := false
		containsCoverage := false
		for {
			part, errPart := reader.NextPart()
			if errPart == io.EOF {
				break
			}
			partName := part.FormName()
			buf := new(bytes.Buffer)
			buf.ReadFrom(part)
			if partName == "event" {
				assert.Equal(t, ContentTypeJSON, part.Header.Get(HeaderContentType))
				assert.Equal(t, "{\"dummy\": true}", string(buf.Bytes()))
				containsDummyEvent = true
			} else if partName == "coveragex" {
				assert.Equal(t, ContentTypeMessagePack, part.Header.Get(HeaderContentType))
				assert.Equal(t, "filecoveragex.msgpack", part.FileName())
				containsCoverage = true
			}
		}

		assert.True(t, containsDummyEvent)
		assert.True(t, containsCoverage)
	}))
	defer server.Close()

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, server.URL)

	cInterface := NewClient()
	var buf bytes.Buffer
	err := cInterface.SendCoveragePayload(&buf)
	assert.Nil(t, err)
}

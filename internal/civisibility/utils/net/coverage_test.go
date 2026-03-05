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
	"path/filepath"
	"testing"

	"github.com/tinylib/msgp/msgp"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	civisibilityutils "github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
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

func TestCoverageApiRequestPayloadFilesModeWritesJSON(t *testing.T) {
	civisibilityutils.ResetTestOptimizationModeForTesting()
	t.Cleanup(civisibilityutils.ResetTestOptimizationModeForTesting)

	outDir := t.TempDir()

	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		http.Error(w, "unexpected network call", http.StatusInternalServerError)
	}))
	defer server.Close()

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, server.URL)
	os.Setenv(constants.CIVisibilityPayloadsInFiles, "true")
	os.Setenv(constants.CIVisibilityUndeclaredOutputsDir, outDir)
	civisibilityutils.ResetTestOptimizationModeForTesting()

	cInterface := NewClient()

	msgpackPayload := msgp.AppendMapHeader(nil, 3)
	msgpackPayload = msgp.AppendString(msgpackPayload, "version")
	msgpackPayload = msgp.AppendInt(msgpackPayload, 2)
	msgpackPayload = msgp.AppendString(msgpackPayload, "metadata")
	msgpackPayload = msgp.AppendMapHeader(msgpackPayload, 0)
	msgpackPayload = msgp.AppendString(msgpackPayload, "coverages")
	msgpackPayload = msgp.AppendArrayHeader(msgpackPayload, 0)

	err := cInterface.SendCoveragePayloadWithFormat(bytes.NewReader(msgpackPayload), FormatMessagePack)
	assert.NoError(t, err)
	assert.Equal(t, 0, hits)

	matches, err := filepath.Glob(filepath.Join(outDir, "payloads", "coverage", "coverage-*.json"))
	assert.NoError(t, err)
	assert.Len(t, matches, 1)

	raw, err := os.ReadFile(matches[0])
	assert.NoError(t, err)

	var payloadMap map[string]any
	assert.NoError(t, json.Unmarshal(raw, &payloadMap))
	assert.Contains(t, payloadMap, "version")
	assert.Contains(t, payloadMap, "metadata")
	assert.Contains(t, payloadMap, "coverages")
}

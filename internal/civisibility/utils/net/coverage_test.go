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
	"strings"
	"testing"

	"github.com/tinylib/msgp/msgp"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/internal/bazel"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
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

func TestNewClientForCodeCoverage(t *testing.T) {
	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, "https://custom.agentless.url")

	cInterface := NewClientForCodeCoverage()
	assert.NotNil(t, cInterface)
	assert.IsType(t, &client{}, cInterface)
}

func TestCoverageApiRequestJSONFormat(t *testing.T) {
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
				assert.Equal(t, ContentTypeJSON, part.Header.Get(HeaderContentType))
				assert.Equal(t, "filecoveragex.json", part.FileName())
				assert.JSONEq(t, `{"version":2,"metadata":{},"coverages":[]}`, buf.String())
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
	err := cInterface.SendCoveragePayloadWithFormat(bytes.NewReader([]byte(`{"version":2,"metadata":{},"coverages":[]}`)), FormatJSON)
	assert.NoError(t, err)
}

func TestCoverageApiRequestPayloadFilesModeWritesJSON(t *testing.T) {
	bazel.ResetForTesting()
	t.Cleanup(bazel.ResetForTesting)

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
	os.Setenv(bazel.PayloadsInFilesEnv, "true")
	os.Setenv(bazel.UndeclaredOutputsDirEnv, outDir)
	bazel.ResetForTesting()

	recordLogger := new(log.RecordLogger)
	oldLevel := log.GetLevel()
	defer log.UseLogger(recordLogger)()
	log.SetLevel(log.LevelDebug)
	defer log.SetLevel(oldLevel)

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
	assert.True(t, containsCoverageLogLine(recordLogger.Logs(), "payload transport mode is file"))
}

func TestCoverageApiRequestPayloadFilesModeWritesJSONFormatPayload(t *testing.T) {
	bazel.ResetForTesting()
	t.Cleanup(bazel.ResetForTesting)

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
	os.Setenv(bazel.PayloadsInFilesEnv, "true")
	os.Setenv(bazel.UndeclaredOutputsDirEnv, outDir)
	bazel.ResetForTesting()

	cInterface := NewClient()
	jsonPayload := []byte(`{"version":2,"metadata":{},"coverages":[]}`)

	err := cInterface.SendCoveragePayloadWithFormat(bytes.NewReader(jsonPayload), FormatJSON)
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

func TestCoverageApiRequestPayloadFilesModeRejectsInvalidJSONFormatPayload(t *testing.T) {
	bazel.ResetForTesting()
	t.Cleanup(bazel.ResetForTesting)

	outDir := t.TempDir()

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, "http://127.0.0.1:1")
	os.Setenv(bazel.PayloadsInFilesEnv, "true")
	os.Setenv(bazel.UndeclaredOutputsDirEnv, outDir)
	bazel.ResetForTesting()

	cInterface := NewClient()
	err := cInterface.SendCoveragePayloadWithFormat(bytes.NewReader([]byte(`{"version":2,`)), FormatJSON)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid coverage json payload")

	matches, globErr := filepath.Glob(filepath.Join(outDir, "payloads", "coverage", "coverage-*.json"))
	assert.NoError(t, globErr)
	assert.Empty(t, matches)
}

func TestCoverageApiRequestPayloadFilesModeRejectsUnsupportedFormat(t *testing.T) {
	bazel.ResetForTesting()
	t.Cleanup(bazel.ResetForTesting)

	outDir := t.TempDir()

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, "http://127.0.0.1:1")
	os.Setenv(bazel.PayloadsInFilesEnv, "true")
	os.Setenv(bazel.UndeclaredOutputsDirEnv, outDir)
	bazel.ResetForTesting()

	cInterface := NewClient()
	err := cInterface.SendCoveragePayloadWithFormat(bytes.NewReader([]byte(`{}`)), "yaml")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported format: yaml")

	matches, globErr := filepath.Glob(filepath.Join(outDir, "payloads", "coverage", "coverage-*.json"))
	assert.NoError(t, globErr)
	assert.Empty(t, matches)
}

func TestCoverageApiRequestPayloadFilesModeMissingOutputDirMsgpack(t *testing.T) {
	bazel.ResetForTesting()
	t.Cleanup(bazel.ResetForTesting)

	tempDir := t.TempDir()
	t.Chdir(tempDir)

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
	os.Setenv(bazel.PayloadsInFilesEnv, "true")
	bazel.ResetForTesting()

	cInterface := NewClient()
	err := cInterface.SendCoveragePayloadWithFormat(bytes.NewReader(testCoverageMsgpackPayload()), FormatMessagePack)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), bazel.UndeclaredOutputsDirEnv)
	assert.Equal(t, 0, hits)

	matches, globErr := filepath.Glob(filepath.Join(tempDir, "payloads", "coverage", "coverage-*.json"))
	assert.NoError(t, globErr)
	assert.Empty(t, matches)
}

func TestCoverageApiRequestPayloadFilesModeMissingOutputDirJSON(t *testing.T) {
	bazel.ResetForTesting()
	t.Cleanup(bazel.ResetForTesting)

	tempDir := t.TempDir()
	t.Chdir(tempDir)

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
	os.Setenv(bazel.PayloadsInFilesEnv, "true")
	bazel.ResetForTesting()

	cInterface := NewClient()
	err := cInterface.SendCoveragePayloadWithFormat(bytes.NewReader([]byte(`{"version":2,"metadata":{},"coverages":[]}`)), FormatJSON)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), bazel.UndeclaredOutputsDirEnv)
	assert.Equal(t, 0, hits)

	matches, globErr := filepath.Glob(filepath.Join(tempDir, "payloads", "coverage", "coverage-*.json"))
	assert.NoError(t, globErr)
	assert.Empty(t, matches)
}

func TestCoverageApiRequestUnexpectedStatusCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "backend rejected payload", http.StatusBadRequest)
	}))
	defer server.Close()

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, server.URL)

	cInterface := NewClient()
	err := cInterface.SendCoveragePayloadWithFormat(bytes.NewReader(testCoverageMsgpackPayload()), FormatMessagePack)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected response code 400")
}

func TestCoverageApiRequestRejectsNilPayload(t *testing.T) {
	c := &client{}
	err := c.SendCoveragePayloadWithFormat(nil, FormatJSON)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "coverage payload is nil")
}

func TestCoverageApiRequestRejectsUnsupportedFormatBeforeSending(t *testing.T) {
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

	cInterface := NewClient()
	err := cInterface.SendCoveragePayloadWithFormat(bytes.NewReader([]byte("{}")), "yaml")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported format: yaml")
	assert.Equal(t, 0, hits)
}

func testCoverageMsgpackPayload() []byte {
	payload := msgp.AppendMapHeader(nil, 3)
	payload = msgp.AppendString(payload, "version")
	payload = msgp.AppendInt(payload, 2)
	payload = msgp.AppendString(payload, "metadata")
	payload = msgp.AppendMapHeader(payload, 0)
	payload = msgp.AppendString(payload, "coverages")
	payload = msgp.AppendArrayHeader(payload, 0)
	return payload
}

func containsCoverageLogLine(lines []string, want string) bool {
	for _, line := range lines {
		if strings.Contains(line, want) {
			return true
		}
	}
	return false
}

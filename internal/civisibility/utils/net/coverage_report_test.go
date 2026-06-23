// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package net

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/bazel"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	civisibilityutils "github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
	coretelemetry "github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

func TestCoverageReportApiRequest(t *testing.T) {
	const lcovReport = "SF:example.go\nDA:1,1\nLH:1\nLF:1\nend_of_record\n"

	var requestContentLength int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/"+coverageReportURLPath, r.URL.Path)
		require.Contains(t, r.Header.Get(HeaderContentType), "multipart/form-data; boundary=")
		require.Empty(t, r.Header.Get(HeaderContentEncoding))
		requestContentLength = r.ContentLength

		parts := readCoverageReportMultipartParts(t, r)
		event := parts["event"]
		require.Equal(t, ContentTypeJSON, event.contentType)
		require.Equal(t, "event.json", event.fileName)

		var eventPayload map[string]string
		require.NoError(t, json.Unmarshal(event.body, &eventPayload))
		require.Equal(t, "coverage_report", eventPayload["type"])
		require.Equal(t, FormatLCOV, eventPayload["format"])
		require.Equal(t, "https://github.com/DataDog/dd-trace-go.git", eventPayload[constants.GitRepositoryURL])
		require.Equal(t, "1234567890abcdef1234567890abcdef12345678", eventPayload[constants.GitCommitSHA])
		require.Equal(t, "main", eventPayload[constants.GitBranch])
		require.Equal(t, "/ci/workspace", eventPayload[constants.CIWorkspacePath])
		require.Equal(t, "42", eventPayload[constants.PrNumber])
		require.NotContains(t, eventPayload, constants.TestSessionName)

		coverage := parts["coverage"]
		require.Equal(t, ContentTypeOctetStream, coverage.contentType)
		require.Equal(t, "coverage.gz", coverage.fileName)
		require.Equal(t, lcovReport, gunzipCoverageReportPart(t, coverage.body))

		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, server.URL)
	civisibilityutils.AddCITagsMap(map[string]string{
		constants.CIWorkspacePath: "/ci/workspace",
		constants.PrNumber:        "42",
		constants.TestSessionName: "not-sent",
	})

	recorder := new(telemetrytest.RecordClient)
	defer coretelemetry.MockClient(recorder)()

	client := NewClientForCoverageReportUpload()
	require.NotNil(t, client)
	require.NoError(t, client.SendCoverageReport(bytes.NewReader([]byte(lcovReport)), FormatLCOV))
	require.Greater(t, requestContentLength, int64(len(lcovReport)))

	requestMetric := telemetrytest.MetricKey{
		Namespace: coretelemetry.NamespaceCIVisibility,
		Name:      "coverage_upload.request",
		Kind:      "count",
	}
	require.Contains(t, recorder.Metrics, requestMetric)
	require.Equal(t, 1.0, recorder.Metrics[requestMetric].Get())
	require.NotContains(t, recorder.Metrics, telemetrytest.MetricKey{
		Namespace: coretelemetry.NamespaceCIVisibility,
		Name:      "coverage_upload.request",
		Tags:      "rq_compressed:true",
		Kind:      "count",
	})

	requestBytesMetric := telemetrytest.MetricKey{
		Namespace: coretelemetry.NamespaceCIVisibility,
		Name:      "coverage_upload.request_bytes",
		Kind:      "distribution",
	}
	require.Contains(t, recorder.Metrics, requestBytesMetric)
	require.Equal(t, float64(requestContentLength), recorder.Metrics[requestBytesMetric].Get())
	require.NotContains(t, recorder.Metrics, telemetrytest.MetricKey{
		Namespace: coretelemetry.NamespaceCIVisibility,
		Name:      "coverage_upload.request_bytes",
		Tags:      "rq_compressed:true",
		Kind:      "distribution",
	})
}

func TestCoverageReportApiRequestRejectsInvalidInput(t *testing.T) {
	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, "https://custom.agentless.url")
	client := NewClientForCoverageReportUpload()
	require.NotNil(t, client)

	require.Error(t, client.SendCoverageReport(nil, FormatLCOV))
	require.Error(t, client.SendCoverageReport(bytes.NewReader([]byte("report")), FormatJSON))
}

func TestCoverageReportApiRequestPayloadFilesModeSkipsNetwork(t *testing.T) {
	bazel.ResetForTesting()
	t.Cleanup(bazel.ResetForTesting)

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
	os.Setenv(bazel.UndeclaredOutputsDirEnv, t.TempDir())
	bazel.ResetForTesting()

	client := NewClientForCoverageReportUpload()
	require.NotNil(t, client)
	require.NoError(t, client.SendCoverageReport(bytes.NewReader([]byte("report")), FormatLCOV))
	require.Equal(t, 0, hits)
}

func TestCoverageReportApiRequestFailsOnNon2xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad report", http.StatusBadRequest)
	}))
	defer server.Close()

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, server.URL)
	client := NewClientForCoverageReportUpload()
	require.NotNil(t, client)

	err := client.SendCoverageReport(bytes.NewReader([]byte("report")), FormatLCOV)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unexpected response code 400")
}

type coverageReportMultipartPart struct {
	body        []byte
	contentType string
	fileName    string
}

func readCoverageReportMultipartParts(t *testing.T, r *http.Request) map[string]coverageReportMultipartPart {
	t.Helper()

	reader, err := r.MultipartReader()
	require.NoError(t, err)

	parts := map[string]coverageReportMultipartPart{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		body, readErr := io.ReadAll(part)
		closeErr := part.Close()
		require.NoError(t, readErr)
		require.NoError(t, closeErr)
		parts[part.FormName()] = coverageReportMultipartPart{
			body:        body,
			contentType: part.Header.Get(HeaderContentType),
			fileName:    part.FileName(),
		}
	}
	return parts
}

func gunzipCoverageReportPart(t *testing.T, body []byte) string {
	t.Helper()

	reader, err := gzip.NewReader(bytes.NewReader(body))
	require.NoError(t, err)
	defer reader.Close()

	uncompressed, err := io.ReadAll(reader)
	require.NoError(t, err)
	return string(uncompressed)
}

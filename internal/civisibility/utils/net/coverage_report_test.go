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
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/bazel"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	civisibilityutils "github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	coretelemetry "github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

func TestCoverageReportApiRequest(t *testing.T) {
	const lcovReport = "SF:example.go\nDA:1,1\nLH:1\nLF:1\nend_of_record\n"

	var requestContentLength int64
	var requestContentType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/"+coverageReportURLPath, r.URL.Path)
		requestContentType = r.Header.Get(HeaderContentType)
		require.Contains(t, requestContentType, "multipart/form-data; boundary=")
		require.Empty(t, r.Header.Get(HeaderContentEncoding))
		rawBody, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, int64(len(rawBody)), r.ContentLength)
		requestContentLength = int64(len(rawBody))
		r.Body = io.NopCloser(bytes.NewReader(rawBody))

		parts := readCoverageReportMultipartParts(t, r)
		event := parts["event"]
		require.Equal(t, ContentTypeJSON, event.contentType)
		require.Equal(t, "event.json", event.fileName)

		var eventPayload map[string]any
		require.NoError(t, json.Unmarshal(event.body, &eventPayload))
		require.Equal(t, "coverage_report", eventPayload["type"])
		require.Equal(t, FormatLCOV, eventPayload["format"])
		require.Equal(t, "https://github.com/DataDog/dd-trace-go.git", eventPayload[constants.GitRepositoryURL])
		require.Equal(t, "1234567890abcdef1234567890abcdef12345678", eventPayload[constants.GitCommitSHA])
		require.Equal(t, "main", eventPayload[constants.GitBranch])
		require.Equal(t, "/ci/workspace", eventPayload[constants.CIWorkspacePath])
		require.Equal(t, "42", eventPayload[constants.PrNumber])
		require.Equal(t, []any{"type:unit-tests", "jvm-21", "type:unit-tests"}, eventPayload["report.flags"])
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
	os.Setenv(constants.CodeCoverageFlagsEnvironmentVariable, "type:unit-tests,jvm-21,type:unit-tests")
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
	require.Contains(t, requestContentType, "multipart/form-data; boundary=")

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

func TestParseCoverageReportFlags(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{name: "unset", want: nil},
		{name: "empty", raw: "", want: nil},
		{name: "comma and whitespace only", raw: " , , ", want: nil},
		{name: "preserves order", raw: "type:unit-tests,jvm-21", want: []string{"type:unit-tests", "jvm-21"}},
		{name: "trims and removes empty entries", raw: " type:unit-tests, , jvm-21 ", want: []string{"type:unit-tests", "jvm-21"}},
		{name: "preserves duplicates", raw: "unit,unit", want: []string{"unit", "unit"}},
		{name: "accepts maximum", raw: makeCoverageReportFlags(maxCoverageReportFlags), want: makeCoverageReportFlagSlice(maxCoverageReportFlags)},
		{name: "rejects over maximum", raw: makeCoverageReportFlags(maxCoverageReportFlags + 1), want: nil},
		{name: "accepts maximum after removing empty entries", raw: " , " + makeCoverageReportFlags(maxCoverageReportFlags) + ", , ", want: makeCoverageReportFlagSlice(maxCoverageReportFlags)},
		{name: "rejects over maximum after removing empty entries", raw: " , " + makeCoverageReportFlags(maxCoverageReportFlags+1) + ", , ", want: nil},
	}

	defer log.UseLogger(log.DiscardLogger{})()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, parseCoverageReportFlags(test.raw))
		})
	}
}

func TestParseCoverageReportFlagsWarnsOnOverflow(t *testing.T) {
	recordLogger := new(log.RecordLogger)
	defer log.UseLogger(recordLogger)()

	require.Nil(t, parseCoverageReportFlags(makeCoverageReportFlags(maxCoverageReportFlags+1)))

	logs := recordLogger.Logs()
	require.Len(t, logs, 1)
	require.Contains(t, logs[0], constants.CodeCoverageFlagsEnvironmentVariable)
	require.Contains(t, logs[0], "33")
	require.Contains(t, logs[0], "32")
}

func TestCoverageReportApiRequestOmitsInvalidFlags(t *testing.T) {
	tests := []struct {
		name  string
		value *string
	}{
		{name: "unset"},
		{name: "empty", value: stringPointer(" , , ")},
		{name: "over maximum", value: stringPointer(makeCoverageReportFlags(maxCoverageReportFlags + 1))},
	}

	defer log.UseLogger(log.DiscardLogger{})()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var requestReceived bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestReceived = true
				parts := readCoverageReportMultipartParts(t, r)
				var eventPayload map[string]any
				require.NoError(t, json.Unmarshal(parts["event"].body, &eventPayload))
				require.NotContains(t, eventPayload, "report.flags")
				w.WriteHeader(http.StatusAccepted)
			}))
			defer server.Close()

			origEnv := saveEnv()
			path := os.Getenv("PATH")
			defer restoreEnv(origEnv)

			setCiVisibilityEnv(path, server.URL)
			if test.value != nil {
				os.Setenv(constants.CodeCoverageFlagsEnvironmentVariable, *test.value)
			}

			client := NewClientForCoverageReportUpload()
			require.NotNil(t, client)
			require.NoError(t, client.SendCoverageReport(bytes.NewReader([]byte("report")), FormatLCOV))
			require.True(t, requestReceived)
		})
	}
}

func TestCoverageReportFlagsAreSnapshotted(t *testing.T) {
	var receivedFlags []any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := readCoverageReportMultipartParts(t, r)
		var eventPayload map[string]any
		require.NoError(t, json.Unmarshal(parts["event"].body, &eventPayload))
		receivedFlags = append(receivedFlags, eventPayload["report.flags"])
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, server.URL)
	os.Setenv(constants.CodeCoverageFlagsEnvironmentVariable, "first,second")
	client := NewClientForCoverageReportUpload()
	require.NotNil(t, client)
	os.Setenv(constants.CodeCoverageFlagsEnvironmentVariable, "later")

	require.NoError(t, client.SendCoverageReport(bytes.NewReader([]byte("first report")), FormatLCOV))
	os.Unsetenv(constants.CodeCoverageFlagsEnvironmentVariable)
	require.NoError(t, client.SendCoverageReport(bytes.NewReader([]byte("second report")), FormatLCOV))
	require.Equal(t, []any{
		[]any{"first", "second"},
		[]any{"first", "second"},
	}, receivedFlags)
}

func TestCoverageReportApiRequestDoesNotPersistMultipartContentTypeHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Contains(t, r.Header.Get(HeaderContentType), "multipart/form-data; boundary=")
		require.Contains(t, readCoverageReportMultipartParts(t, r), "coverage")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, server.URL)
	clientInterface := NewClientForCoverageReportUpload()
	require.NotNil(t, clientInterface)
	client, ok := clientInterface.(*client)
	require.True(t, ok)
	require.NotContains(t, client.headers, HeaderContentType)

	require.NoError(t, client.SendCoverageReport(bytes.NewReader([]byte("report")), FormatLCOV))
	require.NotContains(t, client.headers, HeaderContentType)
}

func TestCoverageReportApiRequestRetainsMultipartBodyOnRetry(t *testing.T) {
	const lcovReport = "SF:retry.go\nDA:1,1\nLH:1\nLF:1\nend_of_record\n"

	var receivedBodies [][]byte
	var receivedContentTypes []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/"+coverageReportURLPath, r.URL.Path)
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		receivedBodies = append(receivedBodies, append([]byte(nil), body...))
		receivedContentTypes = append(receivedContentTypes, r.Header.Get(HeaderContentType))

		if len(receivedBodies) == 1 {
			w.Header().Set(HeaderContentEncoding, ContentEncodingGzip)
			w.Header().Set(HeaderContentType, ContentTypeJSON)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("not valid gzip data"))
			return
		}

		r.Body = io.NopCloser(bytes.NewReader(body))
		parts := readCoverageReportMultipartParts(t, r)
		require.Equal(t, lcovReport, gunzipCoverageReportPart(t, parts["coverage"].body))
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, server.URL)
	client := NewClientForCoverageReportUpload()
	require.NotNil(t, client)
	require.NoError(t, client.SendCoverageReport(bytes.NewReader([]byte(lcovReport)), FormatLCOV))

	require.Len(t, receivedBodies, 2)
	require.Equal(t, receivedBodies[0], receivedBodies[1])
	require.Len(t, receivedContentTypes, 2)
	require.Equal(t, receivedContentTypes[0], receivedContentTypes[1])
	require.Contains(t, receivedContentTypes[0], "multipart/form-data; boundary=")
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

func makeCoverageReportFlags(count int) string {
	return strings.Join(makeCoverageReportFlagSlice(count), ",")
}

func makeCoverageReportFlagSlice(count int) []string {
	flags := make([]string, count)
	for i := range flags {
		flags[i] = "flag-" + strconv.Itoa(i)
	}
	return flags
}

func stringPointer(value string) *string {
	return &value
}

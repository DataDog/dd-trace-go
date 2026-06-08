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
	stdnet "net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/bazel"
	civisibilityutils "github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

func TestClientAgentModeUDSSettingsEndpoint(t *testing.T) {
	runUDSAgentClientEndpointTest(t, NewClient, func(t *testing.T, c *client, w http.ResponseWriter, r *http.Request) {
		assertUDSAgentRequest(t, c, r, "api", settingsURLPath)

		var request settingsRequest
		decodeJSONRequest(t, r, &request)
		require.Equal(t, c.id, request.Data.ID)
		require.Equal(t, settingsRequestType, request.Data.Type)
		require.Equal(t, c.repositoryURL, request.Data.Attributes.RepositoryURL)
		require.Equal(t, c.commitSha, request.Data.Attributes.Sha)

		response := settingsResponse{}
		response.Data.ID = request.Data.ID
		response.Data.Type = settingsRequestType
		response.Data.Attributes.ItrEnabled = true
		response.Data.Attributes.RequireGit = true
		w.Header().Set(HeaderContentType, ContentTypeJSON)
		require.NoError(t, json.NewEncoder(w).Encode(response))
	}, func(t *testing.T, cInterface Client, _ *client) {
		settings, err := cInterface.GetSettings()
		require.NoError(t, err)
		require.True(t, settings.ItrEnabled)
		require.True(t, settings.RequireGit)
	})
}

func TestClientAgentModeUDSKnownTestsEndpoint(t *testing.T) {
	runUDSAgentClientEndpointTest(t, NewClient, func(t *testing.T, c *client, w http.ResponseWriter, r *http.Request) {
		assertUDSAgentRequest(t, c, r, "api", knownTestsURLPath)

		var request knownTestsRequest
		decodeJSONRequest(t, r, &request)
		require.Equal(t, c.id, request.Data.ID)
		require.Equal(t, knownTestsRequestType, request.Data.Type)
		require.Equal(t, c.repositoryURL, request.Data.Attributes.RepositoryURL)
		require.NotNil(t, request.Data.Attributes.PageInfo)

		response := knownTestsResponse{}
		response.Data.ID = request.Data.ID
		response.Data.Type = knownTestsRequestType
		response.Data.Attributes.Tests = KnownTestsResponseDataModules{
			"module": {
				"suite": {"TestUDS"},
			},
		}
		w.Header().Set(HeaderContentType, ContentTypeJSON)
		require.NoError(t, json.NewEncoder(w).Encode(response))
	}, func(t *testing.T, cInterface Client, _ *client) {
		knownTests, err := cInterface.GetKnownTests()
		require.NoError(t, err)
		require.Equal(t, []string{"TestUDS"}, knownTests.Tests["module"]["suite"])
	})
}

func TestClientAgentModeUDSSearchCommitsEndpoint(t *testing.T) {
	runUDSAgentClientEndpointTest(t, NewClient, func(t *testing.T, c *client, w http.ResponseWriter, r *http.Request) {
		assertUDSAgentRequest(t, c, r, "api", searchCommitsURLPath)

		var request searchCommits
		decodeJSONRequest(t, r, &request)
		require.Equal(t, c.repositoryURL, request.Meta.RepositoryURL)
		require.Equal(t, []searchCommitsData{
			{ID: "commit1", Type: searchCommitsType},
			{ID: "commit2", Type: searchCommitsType},
		}, request.Data)

		response := searchCommits{
			Data: []searchCommitsData{
				{ID: "remote1", Type: searchCommitsType},
				{ID: "remote2", Type: searchCommitsType},
			},
		}
		w.Header().Set(HeaderContentType, ContentTypeJSON)
		require.NoError(t, json.NewEncoder(w).Encode(response))
	}, func(t *testing.T, cInterface Client, _ *client) {
		commits, err := cInterface.GetCommits([]string{"commit1", "commit2"})
		require.NoError(t, err)
		require.Equal(t, []string{"remote1", "remote2"}, commits)
	})
}

func TestClientAgentModeUDSSkippableEndpoint(t *testing.T) {
	runUDSAgentClientEndpointTest(t, NewClient, func(t *testing.T, c *client, w http.ResponseWriter, r *http.Request) {
		assertUDSAgentRequest(t, c, r, "api", skippableURLPath)

		var request skippableRequest
		decodeJSONRequest(t, r, &request)
		require.Equal(t, skippableRequestType, request.Data.Type)
		require.Equal(t, c.repositoryURL, request.Data.Attributes.RepositoryURL)
		require.Equal(t, c.commitSha, request.Data.Attributes.Sha)

		response := skippableResponse{
			Meta: skippableResponseMeta{CorrelationID: "uds-correlation-id"},
			Data: []skippableResponseData{
				{
					ID:   "id",
					Type: "test",
					Attributes: SkippableResponseDataAttributes{
						Suite:          "suite",
						Name:           "TestUDS",
						Configurations: c.testConfigurations,
					},
				},
			},
		}
		w.Header().Set(HeaderContentType, ContentTypeJSON)
		require.NoError(t, json.NewEncoder(w).Encode(response))
	}, func(t *testing.T, cInterface Client, _ *client) {
		correlationID, skippables, err := cInterface.GetSkippableTests()
		require.NoError(t, err)
		require.Equal(t, "uds-correlation-id", correlationID)
		require.Len(t, skippables["suite"]["TestUDS"], 1)
	})
}

func TestClientAgentModeUDSTestManagementEndpoint(t *testing.T) {
	runUDSAgentClientEndpointTest(t, NewClient, func(t *testing.T, c *client, w http.ResponseWriter, r *http.Request) {
		assertUDSAgentRequest(t, c, r, "api", testManagementTestsURLPath)

		var request testManagementTestsRequest
		decodeJSONRequest(t, r, &request)
		require.Equal(t, c.id, request.Data.ID)
		require.Equal(t, testManagementTestsRequestType, request.Data.Type)
		require.Equal(t, c.repositoryURL, request.Data.Attributes.RepositoryURL)
		require.Equal(t, c.commitSha, request.Data.Attributes.CommitSha)

		response := testManagementTestsResponse{}
		response.Data.ID = request.Data.ID
		response.Data.Type = testManagementTestsRequestType
		response.Data.Attributes.Modules = map[string]TestManagementTestsResponseDataSuites{
			"module": {
				Suites: map[string]TestManagementTestsResponseDataTests{
					"suite": {
						Tests: map[string]TestManagementTestsResponseDataTestProperties{
							"TestUDS": {
								Properties: TestManagementTestsResponseDataTestPropertiesAttributes{
									AttemptToFix: true,
								},
							},
						},
					},
				},
			},
		}
		w.Header().Set(HeaderContentType, ContentTypeJSON)
		require.NoError(t, json.NewEncoder(w).Encode(response))
	}, func(t *testing.T, cInterface Client, _ *client) {
		tests, err := cInterface.GetTestManagementTests()
		require.NoError(t, err)
		require.True(t, tests.Modules["module"].Suites["suite"].Tests["TestUDS"].Properties.AttemptToFix)
	})
}

func TestClientAgentModeUDSSendPackFilesEndpoint(t *testing.T) {
	packFilePath := filepath.Join(t.TempDir(), "packfile")
	packFileContents := []byte("packfile contents")
	require.NoError(t, os.WriteFile(packFilePath, packFileContents, 0o644))

	runUDSAgentClientEndpointTest(t, NewClient, func(t *testing.T, c *client, w http.ResponseWriter, r *http.Request) {
		assertUDSAgentRequest(t, c, r, "api", sendPackFilesURLPath)

		parts := readMultipartParts(t, r)
		pushedSha := parts["pushedSha"]
		require.Equal(t, ContentTypeJSON, pushedSha.contentType)
		var request pushedShaBody
		require.NoError(t, json.Unmarshal(pushedSha.body, &request))
		require.Equal(t, c.repositoryURL, request.Meta.RepositoryURL)
		require.Equal(t, c.commitSha, request.Data.ID)

		packfile := parts["packfile"]
		require.Equal(t, ContentTypeOctetStream, packfile.contentType)
		require.Equal(t, packFileContents, packfile.body)
		w.WriteHeader(http.StatusOK)
	}, func(t *testing.T, cInterface Client, c *client) {
		bytesSent, err := cInterface.SendPackFiles(c.commitSha, []string{packFilePath})
		require.NoError(t, err)
		require.Equal(t, int64(len(packFileContents)), bytesSent)
	})
}

func TestClientAgentModeUDSCoverageEndpoint(t *testing.T) {
	runUDSAgentClientEndpointTest(t, NewClientForCodeCoverage, func(t *testing.T, c *client, w http.ResponseWriter, r *http.Request) {
		assertUDSAgentRequest(t, c, r, coverageSubDomain, coverageURLPath)

		parts := readMultipartParts(t, r)
		require.Equal(t, ContentTypeJSON, parts["event"].contentType)
		require.JSONEq(t, `{"dummy": true}`, string(parts["event"].body))
		require.Equal(t, ContentTypeJSON, parts["coveragex"].contentType)
		require.Equal(t, "filecoveragex.json", parts["coveragex"].fileName)
		require.JSONEq(t, `{"version":2,"metadata":{},"coverages":[]}`, string(parts["coveragex"].body))
		w.WriteHeader(http.StatusOK)
	}, func(t *testing.T, cInterface Client, _ *client) {
		err := cInterface.SendCoveragePayloadWithFormat(bytes.NewReader([]byte(`{"version":2,"metadata":{},"coverages":[]}`)), FormatJSON)
		require.NoError(t, err)
	})
}

func TestClientAgentModeUDSLogsEndpoint(t *testing.T) {
	runUDSAgentClientEndpointTest(t, NewClientForLogs, func(t *testing.T, c *client, w http.ResponseWriter, r *http.Request) {
		assertUDSAgentRequest(t, c, r, logsSubDomain, logsURLPath)
		require.Equal(t, ContentEncodingGzip, r.Header.Get(HeaderContentEncoding))

		reader, err := gzip.NewReader(r.Body)
		require.NoError(t, err)
		defer reader.Close()
		body, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.JSONEq(t, `{"message":"hello"}`, string(body))
		w.WriteHeader(http.StatusAccepted)
	}, func(t *testing.T, cInterface Client, _ *client) {
		err := cInterface.SendLogs(bytes.NewReader([]byte(`{"message":"hello"}`)))
		require.NoError(t, err)
	})
}

func runUDSAgentClientEndpointTest(
	t *testing.T,
	newClient func() Client,
	handler func(*testing.T, *client, http.ResponseWriter, *http.Request),
	exercise func(*testing.T, Client, *client),
) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Unix domain sockets are not available on Windows")
	}

	var c *client
	var hitsMu sync.Mutex
	hits := 0
	socketURL := newUDSHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/telemetry/proxy/api/v2/apmtelemetry" {
			w.WriteHeader(http.StatusAccepted)
			return
		}

		hitsMu.Lock()
		hits++
		hitsMu.Unlock()

		require.NotNil(t, c)
		handler(t, c, w, r)
	}))
	telemetryInit = sync.Once{}
	restoreTelemetry := telemetry.MockClient(&telemetrytest.RecordClient{})
	t.Cleanup(func() {
		telemetry.StopApp()
		restoreTelemetry()
		telemetryInit = sync.Once{}
	})

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)
	setCiVisibilityUDSAgentEnv(path, socketURL)

	SetReadCacheHooksForTesting(t.TempDir(), nil, nil, nil, nil)
	t.Cleanup(ResetReadCacheHooksForTesting)

	cInterface := newClient()
	require.NotNil(t, cInterface)
	c = cInterface.(*client)
	socketPath := strings.TrimPrefix(socketURL, "unix://")
	require.False(t, c.agentless)
	require.Equal(t, internal.UnixDataSocketURL(socketPath).String(), c.baseURL)
	transport, ok := c.handler.Client.Transport.(*http.Transport)
	require.True(t, ok)
	require.Equal(t, 100, transport.MaxIdleConnsPerHost)

	exercise(t, cInterface, c)
	c.CloseIdleConnections()

	hitsMu.Lock()
	defer hitsMu.Unlock()
	require.Positive(t, hits)
}

func newUDSHTTPServer(t *testing.T, handler http.Handler) string {
	t.Helper()

	socketDir, err := os.MkdirTemp("/tmp", "dd-trace-go-uds-*")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, os.RemoveAll(socketDir))
	})

	socketPath := filepath.Join(socketDir, "apm.socket")
	listener, err := stdnet.Listen("unix", socketPath)
	require.NoError(t, err)

	server := &http.Server{Handler: handler}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(listener)
	}()

	t.Cleanup(func() {
		require.NoError(t, server.Close())
		err := <-errCh
		require.ErrorIs(t, err, http.ErrServerClosed)
	})

	return "unix://" + socketPath
}

func setCiVisibilityUDSAgentEnv(path, socketURL string) {
	os.Clearenv()
	os.Setenv("PATH", path)
	os.Setenv("DD_TRACE_AGENT_URL", socketURL)
	os.Setenv("DD_SERVICE", "uds-service")
	os.Setenv("DD_ENV", "uds-env")
	os.Setenv("DD_GIT_REPOSITORY_URL", "https://github.com/DataDog/dd-trace-go.git")
	os.Setenv("DD_GIT_COMMIT_SHA", "1234567890abcdef1234567890abcdef12345678")
	os.Setenv("DD_GIT_COMMIT_MESSAGE", "fix uds client")
	os.Setenv("DD_GIT_BRANCH", "main")
	civisibilityutils.ResetCITags()
	bazel.ResetForTesting()
}

func assertUDSAgentRequest(t *testing.T, c *client, r *http.Request, subdomain, path string) {
	t.Helper()
	require.Equal(t, http.MethodPost, r.Method)
	require.Equal(t, "/evp_proxy/v2/"+path, r.URL.Path)
	require.Equal(t, subdomain, r.Header.Get("X-Datadog-EVP-Subdomain"))
	require.Equal(t, c.id, r.Header.Get("trace_id"))
	require.Equal(t, c.id, r.Header.Get("parent_id"))
}

func decodeJSONRequest(t *testing.T, r *http.Request, dst any) {
	t.Helper()
	require.Equal(t, ContentTypeJSON, r.Header.Get(HeaderContentType))
	require.NoError(t, json.NewDecoder(r.Body).Decode(dst))
}

type udsMultipartPart struct {
	body        []byte
	contentType string
	fileName    string
}

func readMultipartParts(t *testing.T, r *http.Request) map[string]udsMultipartPart {
	t.Helper()

	reader, err := r.MultipartReader()
	require.NoError(t, err)

	parts := map[string]udsMultipartPart{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		formName := part.FormName()
		contentType := part.Header.Get(HeaderContentType)
		fileName := part.FileName()

		body, readErr := io.ReadAll(part)
		closeErr := part.Close()
		require.NoError(t, readErr)
		require.NoError(t, closeErr)

		parts[formName] = udsMultipartPart{
			body:        body,
			contentType: contentType,
			fileName:    fileName,
		}
	}
	return parts
}

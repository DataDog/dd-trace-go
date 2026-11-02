// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package net

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/bazel"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	coretelemetry "github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

func TestKnownTestsApiRequest(t *testing.T) {
	var c *client
	page1Response := knownTestsResponse{}
	page1Response.Data.Type = settingsRequestType
	page1Response.Data.Attributes.Tests = KnownTestsResponseDataModules{
		"MyModule1": KnownTestsResponseDataSuites{
			"MySuite1": []string{"Test1", "Test2"},
		},
	}
	page1Response.Data.Attributes.PageInfo = &knownTestsResponsePageInfo{
		Cursor:  "cursor_page2",
		Size:    2,
		HasNext: true,
	}

	page2Response := knownTestsResponse{}
	page2Response.Data.Type = settingsRequestType
	page2Response.Data.Attributes.Tests = KnownTestsResponseDataModules{
		"MyModule1": KnownTestsResponseDataSuites{
			"MySuite1": []string{"Test3"},
		},
		"MyModule2": KnownTestsResponseDataSuites{
			"MySuite2": []string{"Test4", "Test5"},
		},
	}
	page2Response.Data.Attributes.PageInfo = &knownTestsResponsePageInfo{
		Cursor:  "cursor_page3",
		Size:    3,
		HasNext: true,
	}

	page3Response := knownTestsResponse{}
	page3Response.Data.Type = settingsRequestType
	page3Response.Data.Attributes.Tests = KnownTestsResponseDataModules{
		"MyModule2": KnownTestsResponseDataSuites{
			"MySuite2": []string{"Test6"},
		},
	}
	// Last page: no PageInfo (nil) means no more pages

	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}

		if r.Header.Get(HeaderContentType) == ContentTypeJSON {
			var request knownTestsRequest
			assert.NoError(t, json.Unmarshal(body, &request))
			assert.Equal(t, c.id, request.Data.ID)
			assert.Equal(t, knownTestsRequestType, request.Data.Type)
			assert.Equal(t, knownTestsURLPath, r.URL.Path[1:])
			assert.Equal(t, c.environment, request.Data.Attributes.Env)
			assert.Equal(t, c.repositoryURL, request.Data.Attributes.RepositoryURL)
			assert.Equal(t, c.serviceName, request.Data.Attributes.Service)
			assert.Equal(t, c.testConfigurations, request.Data.Attributes.Configurations)
			assert.NotNil(t, request.Data.Attributes.PageInfo, "page_info should always be present under attributes")

			var envelope map[string]json.RawMessage
			assert.NoError(t, json.Unmarshal(body, &envelope))
			assert.NotContains(t, envelope, "page_info", "page_info should not be top-level")

			var requestData struct {
				Attributes map[string]json.RawMessage `json:"attributes"`
			}
			assert.NoError(t, json.Unmarshal(envelope["data"], &requestData))
			pageInfoRaw, ok := requestData.Attributes["page_info"]
			assert.True(t, ok, "page_info should be present under data.attributes")
			var pageInfo map[string]any
			assert.NoError(t, json.Unmarshal(pageInfoRaw, &pageInfo))

			w.Header().Set(HeaderContentType, ContentTypeJSON)
			requestCount++
			var resp knownTestsResponse
			switch requestCount {
			case 1:
				assert.Empty(t, request.Data.Attributes.PageInfo.PageState, "first request should have empty page_state")
				assert.Empty(t, pageInfo, "first request should not send pagination fields")
				resp = page1Response
			case 2:
				assert.Equal(t, "cursor_page2", request.Data.Attributes.PageInfo.PageState)
				assert.Equal(t, map[string]any{"page_state": "cursor_page2"}, pageInfo)
				resp = page2Response
			case 3:
				assert.Equal(t, "cursor_page3", request.Data.Attributes.PageInfo.PageState)
				assert.Equal(t, map[string]any{"page_state": "cursor_page3"}, pageInfo)
				resp = page3Response
			default:
				t.Fatalf("unexpected request count: %d", requestCount)
			}
			resp.Data.ID = request.Data.ID
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, server.URL)

	cInterface := NewClient()
	c = cInterface.(*client)
	knownTests, err := cInterface.GetKnownTests()
	assert.Nil(t, err)
	assert.Equal(t, 3, requestCount, "should have made 3 paginated requests")

	// Verify merged results
	assert.Len(t, knownTests.Tests, 2, "should have 2 modules")
	assert.Equal(t, []string{"Test1", "Test2", "Test3"}, knownTests.Tests["MyModule1"]["MySuite1"])
	assert.Equal(t, []string{"Test4", "Test5", "Test6"}, knownTests.Tests["MyModule2"]["MySuite2"])
}

func TestKnownTestsApiRequestFailToUnmarshal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "failed to read body", http.StatusBadRequest)
	}))
	defer server.Close()

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, server.URL)

	cInterface := NewClient()
	efdData, err := cInterface.GetKnownTests()
	assert.Nil(t, efdData)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "cannot unmarshal response")
}

func TestKnownTestsApiRequestFailToGet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal processing error", http.StatusInternalServerError)
	}))
	defer server.Close()

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, server.URL)

	cInterface := NewClient()
	efdData, err := cInterface.GetKnownTests()
	assert.Nil(t, efdData)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "sending known tests request")
}

func TestKnownTestsApiRequestFromManifestCache(t *testing.T) {
	bazel.ResetForTesting()
	t.Cleanup(bazel.ResetForTesting)

	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		http.Error(w, "unexpected network call", http.StatusInternalServerError)
	}))
	defer server.Close()

	expectedResponse := knownTestsResponse{}
	expectedResponse.Data.Attributes.Tests = KnownTestsResponseDataModules{
		"moduleA": {
			"suiteA": {"test1", "test2"},
		},
	}

	cacheDir := filepath.Join(t.TempDir(), ".testoptimization")
	manifestPath := filepath.Join(cacheDir, "manifest.txt")
	if err := os.MkdirAll(filepath.Join(cacheDir, "cache", "http"), 0o755); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}
	if err := os.WriteFile(manifestPath, []byte("1\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	rawResponse, err := json.Marshal(expectedResponse)
	if err != nil {
		t.Fatalf("marshal cache response: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "cache", "http", "known_tests.json"), rawResponse, 0o644); err != nil {
		t.Fatalf("write known tests cache: %v", err)
	}

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)
	setCiVisibilityEnv(path, server.URL)
	os.Setenv(bazel.ManifestFilePathEnv, manifestPath)

	recordLogger := new(log.RecordLogger)
	oldLevel := log.GetLevel()
	defer log.UseLogger(recordLogger)()
	log.SetLevel(log.LevelDebug)
	defer log.SetLevel(oldLevel)

	cInterface := NewClient()
	responseData, err := cInterface.GetKnownTests()
	assert.NoError(t, err)
	assert.Equal(t, expectedResponse.Data.Attributes, *responseData)
	assert.Equal(t, 0, hits)
	assert.True(t, containsLogLine(recordLogger.Logs(), "reading .testoptimization/cache/http/known_tests.json"))
	assert.True(t, containsLogLine(recordLogger.Logs(), "loaded known tests from .testoptimization/cache/http/known_tests.json [modules:1 suites:1 tests:2]"))
}

func TestKnownTestsApiRequestFromManifestCacheMissingFile(t *testing.T) {
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
	if err := os.MkdirAll(filepath.Join(cacheDir, "cache", "http"), 0o755); err != nil {
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
	responseData, err := cInterface.GetKnownTests()
	assert.NoError(t, err)
	assert.Equal(t, KnownTestsResponseData{Tests: KnownTestsResponseDataModules{}}, *responseData)
	assert.Equal(t, 0, hits)
}

func TestKnownTestsApiRequestFromManifestCacheMalformedFile(t *testing.T) {
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
	if err := os.MkdirAll(filepath.Join(cacheDir, "cache", "http"), 0o755); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}
	if err := os.WriteFile(manifestPath, []byte("1\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "cache", "http", "known_tests.json"), []byte("{invalid"), 0o644); err != nil {
		t.Fatalf("write malformed known tests cache: %v", err)
	}

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)
	setCiVisibilityEnv(path, server.URL)
	os.Setenv(bazel.ManifestFilePathEnv, manifestPath)

	recordLogger := new(log.RecordLogger)
	oldLevel := log.GetLevel()
	defer log.UseLogger(recordLogger)()
	log.SetLevel(log.LevelDebug)
	defer log.SetLevel(oldLevel)

	cInterface := NewClient()
	responseData, err := cInterface.GetKnownTests()
	assert.NoError(t, err)
	assert.Equal(t, KnownTestsResponseData{Tests: KnownTestsResponseDataModules{}}, *responseData)
	assert.Equal(t, 0, hits)
	assert.True(t, containsLogLine(recordLogger.Logs(), "invalid known tests file"))
	assert.True(t, containsLogLine(recordLogger.Logs(), "returning empty known tests because manifest cache is unavailable or invalid"))
}

func containsLogLine(lines []string, want string) bool {
	for _, line := range lines {
		if strings.Contains(line, want) {
			return true
		}
	}
	return false
}

type knownTestsRoundTripper func(*http.Request) (*http.Response, error)

func (f knownTestsRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestKnownTestsPaginationMetrics(t *testing.T) {
	for _, pages := range []int{1, 3} {
		t.Run(fmt.Sprintf("%d pages", pages), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				now := time.Now()
				setReadCacheHooksForTest(t, t.TempDir(), &now, 111, 222)
				recorder := new(telemetrytest.RecordClient)
				defer coretelemetry.MockClient(recorder)()
				c := newReadCacheTestClient(nil)
				c.testConfigurations.Custom = nil
				calls := 0
				c.handler = NewRequestHandlerWithClient(&http.Client{Transport: knownTestsRoundTripper(
					func(request *http.Request) (*http.Response, error) {
						defer request.Body.Close()
						var body knownTestsRequest
						require.NoError(t, json.NewDecoder(request.Body).Decode(&body))
						require.NotNil(t, body.Data.Attributes.PageInfo)
						if calls == 0 {
							require.Empty(t, body.Data.Attributes.PageInfo.PageState)
						} else {
							require.Equal(t, strconv.Itoa(calls), body.Data.Attributes.PageInfo.PageState)
						}
						calls++
						time.Sleep(1500 * time.Microsecond)
						response := knownTestsResponse{}
						response.Data.Attributes.Tests = KnownTestsResponseDataModules{"module": {"suite": {"test"}}}
						response.Data.Attributes.PageInfo = &knownTestsResponsePageInfo{
							HasNext: calls < pages, Cursor: strconv.Itoa(calls),
						}
						raw, err := json.Marshal(response)
						require.NoError(t, err)
						return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {ContentTypeJSON}},
							Body: io.NopCloser(bytes.NewReader(raw))}, nil
					}),
				})
				result, err := c.GetKnownTests()
				require.NoError(t, err)
				require.Len(t, result.Tests["module"]["suite"], pages)
				require.Equal(t, pages, calls)
				for name, expected := range map[string]float64{
					"known_tests.pages_fetched":    float64(pages),
					"known_tests.total_fetch_ms":   float64(pages) * 1.5,
					"known_tests.total_request_ms": float64(pages) * 1.5,
				} {
					key := telemetrytest.MetricKey{Namespace: coretelemetry.NamespaceCIVisibility, Name: name, Kind: "distribution"}
					require.Contains(t, recorder.Metrics, key)
					require.Equal(t, expected, recorder.Metrics[key].Get(), name)
				}
				cached, err := c.GetKnownTests()
				require.NoError(t, err)
				require.Equal(t, result, cached)
				require.Equal(t, pages, calls, "cache hit must not fetch more pages")
				key := telemetrytest.MetricKey{Namespace: coretelemetry.NamespaceCIVisibility, Name: "known_tests.pages_fetched", Kind: "distribution"}
				require.Equal(t, float64(pages), recorder.Metrics[key].Get(), "cache hit must not submit another sample")
			})
		})
	}
}

func TestKnownTestsPaginationMetricsOmittedOnFailure(t *testing.T) {
	for _, failure := range []string{"transport", "malformed JSON", "HTTP error with JSON"} {
		t.Run(failure, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				now := time.Now()
				setReadCacheHooksForTest(t, t.TempDir(), &now, 111, 222)
				recorder := new(telemetrytest.RecordClient)
				defer coretelemetry.MockClient(recorder)()
				c := newReadCacheTestClient(nil)
				c.testConfigurations.Custom = nil
				calls := 0
				c.handler = NewRequestHandlerWithClient(&http.Client{Transport: knownTestsRoundTripper(
					func(request *http.Request) (*http.Response, error) {
						request.Body.Close()
						calls++
						time.Sleep(time.Millisecond)
						if calls == 1 {
							return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {ContentTypeJSON}},
								Body: io.NopCloser(strings.NewReader(`{"data":{"attributes":{"tests":{"module":{"suite":["test"]}},"page_info":{"has_next":true,"cursor":"second"}}}}`))}, nil
						}
						if failure == "transport" {
							return nil, errors.New("connection reset")
						}
						status, body := http.StatusOK, "{invalid"
						if failure == "HTTP error with JSON" {
							status, body = http.StatusBadRequest, `{"data":{"attributes":{"tests":{}}}}`
						}
						return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {ContentTypeJSON}},
							Body: io.NopCloser(strings.NewReader(body))}, nil
					}),
				})
				_, err := c.GetKnownTests()
				if failure != "HTTP error with JSON" {
					require.Error(t, err)
				}
				for _, name := range []string{"known_tests.pages_fetched", "known_tests.total_fetch_ms", "known_tests.total_request_ms"} {
					require.NotContains(t, recorder.Metrics, telemetrytest.MetricKey{
						Namespace: coretelemetry.NamespaceCIVisibility, Name: name, Kind: "distribution",
					})
				}
			})
		})
	}
}

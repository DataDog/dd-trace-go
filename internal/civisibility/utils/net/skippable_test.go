// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package net

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/internal/bazel"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/filebitmap"
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
	response, err := cInterface.GetSkippableTests()
	assert.Nil(t, err)
	assert.Equal(t, "correlation_id", response.CorrelationID)
	skippables := response.Skippables
	assert.Len(t, skippables, 1)
	assert.Len(t, skippables["suite"], 1)
	if assert.Contains(t, skippables["suite"], "name") {
		assert.Len(t, skippables["suite"]["name"], 1)
		assert.Equal(t, expectedResponse.Data[0].Attributes, skippables["suite"]["name"][0])
	}
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
	response, err := cInterface.GetSkippableTests()
	assert.Nil(t, response)
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
	response, err := cInterface.GetSkippableTests()
	assert.Nil(t, response)
	assert.Contains(t, err.Error(), "sending skippable tests request")
}

func TestSkippableApiRequestFromManifestModeIgnoresCache(t *testing.T) {
	bazel.ResetForTesting()
	t.Cleanup(bazel.ResetForTesting)

	validCache, err := json.Marshal(skippableResponse{
		Meta: skippableResponseMeta{
			CorrelationID: "cache-correlation-id",
		},
		Data: []skippableResponseData{
			{
				ID:   "id-1",
				Type: "test",
				Attributes: SkippableResponseDataAttributes{
					Suite: "suite",
					Name:  "match",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal skippable cache: %v", err)
	}

	testCases := []struct {
		name          string
		writeCache    bool
		cacheContents []byte
	}{
		{name: "valid cache", writeCache: true, cacheContents: validCache},
		{name: "missing cache"},
		{name: "malformed cache", writeCache: true, cacheContents: []byte("{invalid")},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
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
			if testCase.writeCache {
				if err := os.WriteFile(filepath.Join(cacheDir, "cache", "http", "skippable_tests.json"), testCase.cacheContents, 0o644); err != nil {
					t.Fatalf("write skippable cache: %v", err)
				}
			}

			origEnv := saveEnv()
			path := os.Getenv("PATH")
			defer restoreEnv(origEnv)
			setCiVisibilityEnv(path, server.URL)
			os.Setenv(bazel.ManifestFilePathEnv, manifestPath)

			cInterface := NewClient()
			response, err := cInterface.GetSkippableTests()
			assert.NoError(t, err)
			assert.Equal(t, "", response.CorrelationID)
			assert.Equal(t, map[string]map[string][]SkippableResponseDataAttributes{}, response.Skippables)
			assert.Equal(t, 0, hits)
		})
	}
}

func TestSkippableApiRequestParsesCoverageMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(HeaderContentType, ContentTypeJSON)
		_ = json.NewEncoder(w).Encode(skippableResponse{
			Meta: skippableResponseMeta{
				CorrelationID: "correlation_id",
				Coverage: json.RawMessage(`{
						"/pkg/file.go": "` + base64.StdEncoding.EncodeToString([]byte{0b10000000}) + `",
						"pkg\\file.go": "` + base64.StdEncoding.EncodeToString([]byte{0b01000000}) + `",
						"pkg/line8.go": "` + base64.StdEncoding.EncodeToString([]byte{0b00000001}) + `"
				}`),
			},
			Data: []skippableResponseData{
				{
					Attributes: SkippableResponseDataAttributes{
						Suite:                   "suite",
						Name:                    "name",
						MissingLineCodeCoverage: true,
					},
				},
			},
		})
	}))
	defer server.Close()

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, server.URL)

	response, err := NewClient().GetSkippableTests()
	assert.NoError(t, err)
	assert.True(t, response.CoveragePresent)
	assert.True(t, response.CoverageBackfillSafe)
	assert.Contains(t, response.Coverage, "pkg/file.go")
	assert.True(t, response.Coverage["pkg/file.go"].Get(1))
	assert.True(t, response.Coverage["pkg/file.go"].Get(2))
	assert.True(t, response.Coverage["pkg/line8.go"].Get(8))
	assert.True(t, response.Skippables["suite"]["name"][0].MissingLineCodeCoverage)
}

func TestSkippableCoverageUsesFileBitmapByteOrder(t *testing.T) {
	decoded := filebitmap.NewFileBitmapFromBytes([]byte{0b10000001})
	assert.True(t, decoded.Get(1))
	assert.True(t, decoded.Get(8))
	assert.False(t, decoded.Get(2))

	assert.Equal(t, []byte{0b10000000}, filebitmap.FromActiveRange(1, 1).ToArray())
	assert.Equal(t, []byte{0b00000001}, filebitmap.FromActiveRange(8, 8).ToArray())
}

func TestSkippableApiRequestCoverageMetadataPresenceStates(t *testing.T) {
	for _, test := range []struct {
		name         string
		coverageJSON string
		wantPresent  bool
		wantSafe     bool
		wantReason   string
		omitCoverage bool
	}{
		{name: "absent", omitCoverage: true, wantPresent: false, wantSafe: false, wantReason: coverageBackfillReasonMissing},
		{name: "null", coverageJSON: "null", wantPresent: false, wantSafe: false, wantReason: coverageBackfillReasonMissing},
		{name: "empty", coverageJSON: "{}", wantPresent: true, wantSafe: false, wantReason: coverageBackfillReasonEmpty},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set(HeaderContentType, ContentTypeJSON)
				if test.omitCoverage {
					_ = json.NewEncoder(w).Encode(struct {
						Meta struct {
							CorrelationID string `json:"correlation_id"`
						} `json:"meta"`
						Data []skippableResponseData `json:"data"`
					}{
						Meta: struct {
							CorrelationID string `json:"correlation_id"`
						}{CorrelationID: "correlation_id"},
						Data: []skippableResponseData{{Attributes: SkippableResponseDataAttributes{Suite: "suite", Name: "name"}}},
					})
					return
				}
				_ = json.NewEncoder(w).Encode(skippableResponse{
					Meta: skippableResponseMeta{
						CorrelationID: "correlation_id",
						Coverage:      json.RawMessage(test.coverageJSON),
					},
					Data: []skippableResponseData{{Attributes: SkippableResponseDataAttributes{Suite: "suite", Name: "name"}}},
				})
			}))
			defer server.Close()

			origEnv := saveEnv()
			path := os.Getenv("PATH")
			defer restoreEnv(origEnv)

			setCiVisibilityEnv(path, server.URL)

			response, err := NewClient().GetSkippableTests()
			assert.NoError(t, err)
			assert.Equal(t, test.wantPresent, response.CoveragePresent)
			assert.Equal(t, test.wantSafe, response.CoverageBackfillSafe)
			assert.Equal(t, test.wantReason, response.CoverageBackfillReason)
			assert.Contains(t, response.Skippables["suite"], "name")
		})
	}
}

func TestSkippableApiRequestMalformedCoverageIsUnsafeWithoutFailingRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(HeaderContentType, ContentTypeJSON)
		_ = json.NewEncoder(w).Encode(struct {
			Meta struct {
				CorrelationID string          `json:"correlation_id"`
				Coverage      json.RawMessage `json:"coverage"`
			} `json:"meta"`
			Data []skippableResponseData `json:"data"`
		}{
			Meta: struct {
				CorrelationID string          `json:"correlation_id"`
				Coverage      json.RawMessage `json:"coverage"`
			}{
				CorrelationID: "correlation_id",
				Coverage:      json.RawMessage(`{"pkg/file.go": 123}`),
			},
			Data: []skippableResponseData{{Attributes: SkippableResponseDataAttributes{Suite: "suite", Name: "name"}}},
		})
	}))
	defer server.Close()

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, server.URL)

	response, err := NewClient().GetSkippableTests()
	assert.NoError(t, err)
	assert.Equal(t, "correlation_id", response.CorrelationID)
	assert.True(t, response.CoveragePresent)
	assert.False(t, response.CoverageBackfillSafe)
	assert.Equal(t, coverageBackfillReasonInvalid, response.CoverageBackfillReason)
	assert.Contains(t, response.Skippables["suite"], "name")
}

func TestSkippableApiRequestRejectsNonRepositoryRelativeCoveragePaths(t *testing.T) {
	for _, test := range []struct {
		name string
		path string
	}{
		{name: "windows-drive", path: "C:\\repo\\pkg\\file.go"},
		{name: "traversal", path: "pkg/../..//file.go"},
	} {
		t.Run(test.name, func(t *testing.T) {
			coverage := base64.StdEncoding.EncodeToString([]byte{0b10000000})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set(HeaderContentType, ContentTypeJSON)
				_ = json.NewEncoder(w).Encode(skippableResponse{
					Meta: skippableResponseMeta{
						CorrelationID: "correlation_id",
						Coverage:      json.RawMessage(`{"` + strings.ReplaceAll(test.path, `\`, `\\`) + `":"` + coverage + `"}`),
					},
					Data: []skippableResponseData{{Attributes: SkippableResponseDataAttributes{Suite: "suite", Name: "name"}}},
				})
			}))
			defer server.Close()

			origEnv := saveEnv()
			path := os.Getenv("PATH")
			defer restoreEnv(origEnv)

			setCiVisibilityEnv(path, server.URL)

			response, err := NewClient().GetSkippableTests()
			assert.NoError(t, err)
			assert.True(t, response.CoveragePresent)
			assert.False(t, response.CoverageBackfillSafe)
			assert.Equal(t, coverageBackfillReasonInvalid, response.CoverageBackfillReason)
			assert.Contains(t, response.Skippables["suite"], "name")
		})
	}
}

func TestSkippableApiRequestDoesNotFilterConfigurations(t *testing.T) {
	coverage := base64.StdEncoding.EncodeToString([]byte{0b10000000})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(HeaderContentType, ContentTypeJSON)
		_ = json.NewEncoder(w).Encode(skippableResponse{
			Meta: skippableResponseMeta{
				CorrelationID: "correlation_id",
				Coverage:      json.RawMessage(`{"pkg/file.go":"` + coverage + `"}`),
			},
			Data: []skippableResponseData{
				{Attributes: SkippableResponseDataAttributes{Suite: "suite", Name: "linux", Configurations: testConfigurations{OsPlatform: "linux"}}},
				{Attributes: SkippableResponseDataAttributes{Suite: "suite", Name: "darwin", Configurations: testConfigurations{OsPlatform: "darwin"}}},
				{Attributes: SkippableResponseDataAttributes{Suite: "suite", Name: "custom", Configurations: testConfigurations{Custom: map[string]string{"region": "eu"}}}},
			},
		})
	}))
	defer server.Close()

	origEnv := saveEnv()
	path := os.Getenv("PATH")
	defer restoreEnv(origEnv)

	setCiVisibilityEnv(path, server.URL)
	client := NewClient().(*client)
	client.testConfigurations.OsPlatform = "linux"
	client.testConfigurations.Custom = map[string]string{"region": "us"}

	response, err := client.GetSkippableTests()
	assert.NoError(t, err)
	assert.True(t, response.CoveragePresent)
	assert.True(t, response.CoverageBackfillSafe)
	assert.Empty(t, response.CoverageBackfillReason)
	assert.Contains(t, response.Skippables["suite"], "linux")
	assert.Contains(t, response.Skippables["suite"], "darwin")
	assert.Contains(t, response.Skippables["suite"], "custom")
}

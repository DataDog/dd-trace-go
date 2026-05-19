// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package net

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/env"
)

func TestReadCacheScopeIdentitySelectionAndTTL(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	setReadCacheHooksForTest(t, t.TempDir(), &now, 111, 222)

	local := newReadCacheScopeIdentity(map[string]string{})
	require.Equal(t, readCacheScopeLocal, local.Kind)
	require.Equal(t, time.Minute, local.TTL)
	require.Equal(t, []string{"parent_pid"}, local.Source)

	now = time.Unix(1_700_000_000, 0)
	setReadCacheHooksForTest(t, t.TempDir(), &now, 111, 333)
	otherLocal := newReadCacheScopeIdentity(map[string]string{})
	assert.NotEqual(t, local.Hash, otherLocal.Hash)

	weak := newReadCacheScopeIdentity(map[string]string{
		constants.CIProviderName:   " github ",
		constants.CIPipelineID:     "pipeline-id",
		constants.CIPipelineNumber: "pipeline-number",
	})
	require.Equal(t, readCacheScopeCIWeak, weak.Kind)
	require.Equal(t, 5*time.Minute, weak.TTL)
	require.Equal(t, []string{constants.CIProviderName, constants.CIPipelineID}, weak.Source)

	weakWithChangedUnusedPipelineNumber := newReadCacheScopeIdentity(map[string]string{
		constants.CIProviderName:   "github",
		constants.CIPipelineID:     "pipeline-id",
		constants.CIPipelineNumber: "other-number",
	})
	assert.Equal(t, weak.Hash, weakWithChangedUnusedPipelineNumber.Hash)

	medium := newReadCacheScopeIdentity(map[string]string{
		constants.CIProviderName:   "github",
		constants.CIPipelineNumber: "42",
		constants.CIJobName:        "unit",
		constants.CIStageName:      "test",
		constants.CINodeName:       "node-1",
	})
	require.Equal(t, readCacheScopeCIMedium, medium.Kind)
	require.Equal(t, 15*time.Minute, medium.TTL)
	require.Equal(t, []string{constants.CIProviderName, constants.CIPipelineNumber, constants.CIJobName, constants.CIStageName, constants.CINodeName}, medium.Source)

	strong := newReadCacheScopeIdentity(map[string]string{
		constants.CIProviderName: "github",
		constants.CIPipelineID:   "pipeline-id",
		constants.CIJobID:        "job-id",
		constants.CIJobName:      "job-name",
	})
	require.Equal(t, readCacheScopeCIStrong, strong.Kind)
	require.Equal(t, time.Hour, strong.TTL)
	require.Equal(t, []string{constants.CIProviderName, constants.CIPipelineID, constants.CIJobID}, strong.Source)

	providerAndJobNameOnly := newReadCacheScopeIdentity(map[string]string{
		constants.CIProviderName: "github",
		constants.CIJobName:      "job-name",
	})
	require.Equal(t, readCacheScopeLocal, providerAndJobNameOnly.Kind)
}

func TestReadThroughShortLivedCacheWritesSanitizedEntryAndHits(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	root := t.TempDir()
	setReadCacheHooksForTest(t, root, &now, 111, 222)

	c := newReadCacheTestClient(map[string]string{})
	c.repositoryURL = "https://user:password@github.com/acme/private.git"
	c.baseURL = "https://token@example.com/path/to/api?api_key=secret#fragment"
	c.testConfigurations.Custom = map[string]string{"Region": "us1"}

	liveCalls := 0
	live := func() (readCacheLiveResult[string], error) {
		liveCalls++
		return readCacheLiveResult[string]{Value: "from-live", Cacheable: true}, nil
	}

	value, err := readThroughShortLivedCache(c, "unit", map[string]any{"stable": "request"}, live, nil)
	require.NoError(t, err)
	require.Equal(t, "from-live", value)

	value, err = readThroughShortLivedCache(c, "unit", map[string]any{"stable": "request"}, live, nil)
	require.NoError(t, err)
	require.Equal(t, "from-live", value)
	require.Equal(t, 1, liveCalls)

	cacheFiles := readCacheJSONFiles(t, root)
	require.Len(t, cacheFiles, 1)
	raw, err := os.ReadFile(cacheFiles[0])
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "user:password")
	assert.NotContains(t, string(raw), "acme/private")
	assert.NotContains(t, string(raw), "token")
	assert.NotContains(t, string(raw), "api_key=secret")

	var entry readCacheEntry[string]
	require.NoError(t, json.Unmarshal(raw, &entry))
	assert.Equal(t, "https://example.com", entry.BaseScope.BaseURLSanitized)
	assertLowerHex(t, entry.CacheKey)
	assertLowerHex(t, entry.BaseScope.RepositoryURLHash)
	assertLowerHex(t, entry.BaseScope.BaseURLHash)
	assertLowerHex(t, entry.BaseScope.ScopeIdentityHash)
	assertLowerHex(t, entry.EndpointScope.RequestHash)
}

func TestReadThroughShortLivedCacheScopeChangeMisses(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	root := t.TempDir()
	setReadCacheHooksForTest(t, root, &now, 111, 222)

	liveCalls := 0
	live := func() (readCacheLiveResult[string], error) {
		liveCalls++
		return readCacheLiveResult[string]{Value: "value", Cacheable: true}, nil
	}

	firstClient := newReadCacheTestClient(map[string]string{})
	_, err := readThroughShortLivedCache(firstClient, "unit", map[string]string{"request": "same"}, live, nil)
	require.NoError(t, err)

	setReadCacheHooksForTest(t, root, &now, 111, 333)
	secondClient := newReadCacheTestClient(map[string]string{})
	_, err = readThroughShortLivedCache(secondClient, "unit", map[string]string{"request": "same"}, live, nil)
	require.NoError(t, err)

	require.Equal(t, 2, liveCalls)
}

func TestReadThroughShortLivedCacheRejectsInvalidExpiredAndWrongTTL(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	root := t.TempDir()
	setReadCacheHooksForTest(t, root, &now, 111, 222)

	c := newReadCacheTestClient(map[string]string{})
	semanticRequest := map[string]string{"request": "stable"}
	cacheKey, paths := readCacheTestKeyAndPaths(t, c, "unit", semanticRequest)

	liveCalls := 0
	live := func() (readCacheLiveResult[string], error) {
		liveCalls++
		return readCacheLiveResult[string]{Value: "fresh", Cacheable: true}, nil
	}

	require.NoError(t, os.WriteFile(paths.CacheFile, []byte("{invalid"), 0o600))
	value, err := readThroughShortLivedCache(c, "unit", semanticRequest, live, nil)
	require.NoError(t, err)
	require.Equal(t, "fresh", value)
	require.Equal(t, 1, liveCalls)

	baseScope := c.readCacheBaseScope()
	requestHash, err := readCacheHashJSON(semanticRequest)
	require.NoError(t, err)
	endpointScope := readCacheEndpointScope{Endpoint: "unit", EndpointVersion: readCacheEndpointVersion, RequestHash: requestHash}
	expiredEntry := readCacheEntry[string]{
		CacheKey:          cacheKey,
		CreatedAtUnixNano: now.Add(-2 * time.Minute).UnixNano(),
		TTLSeconds:        int64(time.Minute.Seconds()),
		BaseScope:         baseScope,
		EndpointScope:     endpointScope,
		Response:          "expired",
	}
	writeReadCacheTestEntry(t, paths.CacheFile, expiredEntry)
	value, err = readThroughShortLivedCache(c, "unit", semanticRequest, live, nil)
	require.NoError(t, err)
	require.Equal(t, "fresh", value)
	require.Equal(t, 2, liveCalls)

	wrongTTLEntry := expiredEntry
	wrongTTLEntry.CreatedAtUnixNano = now.UnixNano()
	wrongTTLEntry.TTLSeconds = int64((5 * time.Minute).Seconds())
	writeReadCacheTestEntry(t, paths.CacheFile, wrongTTLEntry)
	value, err = readThroughShortLivedCache(c, "unit", semanticRequest, live, nil)
	require.NoError(t, err)
	require.Equal(t, "fresh", value)
	require.Equal(t, 3, liveCalls)
}

func TestReadThroughShortLivedCacheDoesNotStoreNonCacheableResponses(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	root := t.TempDir()
	setReadCacheHooksForTest(t, root, &now, 111, 222)

	c := newReadCacheTestClient(map[string]string{})
	liveCalls := 0
	live := func() (readCacheLiveResult[string], error) {
		liveCalls++
		return readCacheLiveResult[string]{Value: "non-cacheable", Cacheable: false}, nil
	}

	for range 2 {
		value, err := readThroughShortLivedCache(c, "unit", map[string]string{"request": "same"}, live, nil)
		require.NoError(t, err)
		require.Equal(t, "non-cacheable", value)
	}
	require.Equal(t, 2, liveCalls)
	require.Empty(t, readCacheJSONFiles(t, root))
}

func TestReadCacheActiveLockDetection(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	root := t.TempDir()
	setReadCacheHooksForTest(t, root, &now, 111, 222)

	c := newReadCacheTestClient(map[string]string{})
	semanticRequest := map[string]string{"request": "same"}
	cacheKey, paths := readCacheTestKeyAndPaths(t, c, "unit", semanticRequest)
	owner, status := acquireReadCacheLock(paths, cacheKey)
	require.Equal(t, readCacheLockAcquired, status)
	require.NotNil(t, owner)
	require.FileExists(t, paths.LockFile)

	otherOwner, activeStatus := acquireReadCacheLock(paths, cacheKey)
	require.Nil(t, otherOwner)
	require.Equal(t, readCacheLockActive, activeStatus)

	releaseReadCacheLock(owner)
	reacquiredOwner, reacquiredStatus := acquireReadCacheLock(paths, cacheKey)
	require.Equal(t, readCacheLockAcquired, reacquiredStatus)
	require.NotNil(t, reacquiredOwner)
	releaseReadCacheLock(reacquiredOwner)
}

func TestReadCacheStaleOwnerDoesNotOverwriteReplacementLock(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	root := t.TempDir()
	setReadCacheHooksForTest(t, root, &now, 111, 222)

	c := newReadCacheTestClient(map[string]string{})
	cacheKey, paths := readCacheTestKeyAndPaths(t, c, "unit", map[string]string{"request": "same"})
	owner, status := acquireReadCacheLock(paths, cacheKey)
	require.Equal(t, readCacheLockAcquired, status)
	require.NotNil(t, owner)

	replacement := readCacheLockEntry{
		PID:               999,
		CreatedAtUnixNano: now.UnixNano(),
		CacheKey:          cacheKey,
		OwnerNonce:        "0123456789abcdef0123456789abcdef",
	}
	raw, err := json.Marshal(replacement)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(paths.LockFile, raw, 0o600))

	writeReadCacheEntry(paths, owner, cacheKey, c.readCacheBaseScope(), readCacheEndpointScope{Endpoint: "unit", EndpointVersion: readCacheEndpointVersion}, time.Minute, "stale")
	_, err = os.Stat(paths.CacheFile)
	require.True(t, os.IsNotExist(err))
	releaseReadCacheLock(owner)

	current, err := readCacheLockEntryFromFile(paths.LockFile)
	require.NoError(t, err)
	require.Equal(t, replacement.OwnerNonce, current.OwnerNonce)
}

func TestReadCacheCurrentKeyCleanupRemovesOnlyStaleTemporaryFiles(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	root := t.TempDir()
	setReadCacheHooksForTest(t, root, &now, 111, 222)

	c := newReadCacheTestClient(map[string]string{})
	_, paths := readCacheTestKeyAndPaths(t, c, "unit", map[string]string{"request": "same"})
	staleTmp := filepath.Join(paths.Dir, "abc.tmp.1.stale")
	freshTmp := filepath.Join(paths.Dir, "abc.tmp.1.fresh")
	require.NoError(t, os.WriteFile(staleTmp, []byte("stale"), 0o600))
	require.NoError(t, os.WriteFile(freshTmp, []byte("fresh"), 0o600))
	staleTime := now.Add(-readCacheStaleLockTimeout - time.Minute)
	require.NoError(t, os.Chtimes(staleTmp, staleTime, staleTime))

	readCacheCleanupCurrentKey(readCachePaths{
		Dir:     paths.Dir,
		TmpGlob: filepath.Join(paths.Dir, "*.tmp.*"),
	})

	_, err := os.Stat(staleTmp)
	require.True(t, os.IsNotExist(err))
	_, err = os.Stat(freshTmp)
	require.NoError(t, err)
}

func TestReadCacheGetSettingsCachesSuccessfulResponseAndSkipsRequireGit(t *testing.T) {
	t.Run("successful settings response is cached", func(t *testing.T) {
		now := time.Unix(1_700_000_000, 0)
		root := t.TempDir()
		var requestCount atomic.Int64
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/"+settingsURLPath, r.URL.Path)
			requestCount.Add(1)
			w.Header().Set(HeaderContentType, ContentTypeJSON)
			require.NoError(t, json.NewEncoder(w).Encode(settingsResponse{
				Data: struct {
					ID         string               `json:"id"`
					Type       string               `json:"type"`
					Attributes SettingsResponseData `json:"attributes"`
				}{
					Type: settingsRequestType,
					Attributes: SettingsResponseData{
						CodeCoverage:  true,
						TestsSkipping: true,
					},
				},
			}))
		}))
		defer server.Close()
		setupReadCacheEndpointEnv(t, server.URL, root, &now)

		first := NewClient()
		require.NotNil(t, first)
		settings, err := first.GetSettings()
		require.NoError(t, err)
		require.True(t, settings.CodeCoverage)

		second := NewClient()
		require.NotNil(t, second)
		settings, err = second.GetSettings()
		require.NoError(t, err)
		require.True(t, settings.CodeCoverage)
		require.Equal(t, int64(1), requestCount.Load())
	})

	t.Run("require git response is not cached", func(t *testing.T) {
		now := time.Unix(1_700_000_000, 0)
		root := t.TempDir()
		var requestCount atomic.Int64
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requestCount.Add(1)
			w.Header().Set(HeaderContentType, ContentTypeJSON)
			require.NoError(t, json.NewEncoder(w).Encode(settingsResponse{
				Data: struct {
					ID         string               `json:"id"`
					Type       string               `json:"type"`
					Attributes SettingsResponseData `json:"attributes"`
				}{
					Type: settingsRequestType,
					Attributes: SettingsResponseData{
						RequireGit: true,
					},
				},
			}))
		}))
		defer server.Close()
		setupReadCacheEndpointEnv(t, server.URL, root, &now)

		_, err := NewClient().GetSettings()
		require.NoError(t, err)
		_, err = NewClient().GetSettings()
		require.NoError(t, err)
		require.Equal(t, int64(2), requestCount.Load())
		require.Empty(t, readCacheJSONFiles(t, root))
	})
}

func TestReadCacheGetKnownTestsCachesAccumulatedPages(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	root := t.TempDir()
	var requestCount atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var request knownTestsRequest
		require.NoError(t, json.Unmarshal(body, &request))

		w.Header().Set(HeaderContentType, ContentTypeJSON)
		count := requestCount.Add(1)
		response := knownTestsResponse{}
		response.Data.Type = knownTestsRequestType
		switch count {
		case 1:
			require.Empty(t, request.PageInfo.PageState)
			response.Data.Attributes.Tests = KnownTestsResponseDataModules{"module": {"suite": {"TestOne"}}}
			response.PageInfo = &knownTestsResponsePageInfo{Cursor: "next", HasNext: true}
		case 2:
			require.Equal(t, "next", request.PageInfo.PageState)
			response.Data.Attributes.Tests = KnownTestsResponseDataModules{"module": {"suite": {"TestTwo"}}}
		default:
			t.Fatalf("unexpected known tests request %d", count)
		}
		require.NoError(t, json.NewEncoder(w).Encode(response))
	}))
	defer server.Close()
	setupReadCacheEndpointEnv(t, server.URL, root, &now)

	first, err := NewClient().GetKnownTests()
	require.NoError(t, err)
	require.Equal(t, []string{"TestOne", "TestTwo"}, first.Tests["module"]["suite"])

	second, err := NewClient().GetKnownTests()
	require.NoError(t, err)
	require.Equal(t, []string{"TestOne", "TestTwo"}, second.Tests["module"]["suite"])
	require.Equal(t, int64(2), requestCount.Load())
}

func TestReadCacheGetSkippableTestsCachesFilteredResponse(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	root := t.TempDir()
	var requestCount atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCount.Add(1)
		w.Header().Set(HeaderContentType, ContentTypeJSON)
		require.NoError(t, json.NewEncoder(w).Encode(skippableResponse{
			Meta: skippableResponseMeta{CorrelationID: "correlation-id"},
			Data: []skippableResponseData{
				{Attributes: SkippableResponseDataAttributes{Suite: "suite", Name: "linux", Configurations: testConfigurations{OsPlatform: "linux"}}},
				{Attributes: SkippableResponseDataAttributes{Suite: "suite", Name: "darwin", Configurations: testConfigurations{OsPlatform: "darwin"}}},
			},
		}))
	}))
	defer server.Close()
	setupReadCacheEndpointEnv(t, server.URL, root, &now)

	first := NewClient().(*client)
	first.testConfigurations.OsPlatform = "linux"
	correlationID, skippables, err := first.GetSkippableTests()
	require.NoError(t, err)
	require.Equal(t, "correlation-id", correlationID)
	require.Contains(t, skippables["suite"], "linux")
	require.NotContains(t, skippables["suite"], "darwin")

	second := NewClient().(*client)
	second.testConfigurations.OsPlatform = "linux"
	correlationID, skippables, err = second.GetSkippableTests()
	require.NoError(t, err)
	require.Equal(t, "correlation-id", correlationID)
	require.Contains(t, skippables["suite"], "linux")
	require.Equal(t, int64(1), requestCount.Load())

	third := NewClient().(*client)
	third.testConfigurations.OsPlatform = "darwin"
	correlationID, skippables, err = third.GetSkippableTests()
	require.NoError(t, err)
	require.Equal(t, "correlation-id", correlationID)
	require.Contains(t, skippables["suite"], "darwin")
	require.Equal(t, int64(2), requestCount.Load())
}

func TestReadCacheGetTestManagementCachesWithoutNewCommitPrecondition(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	root := t.TempDir()
	var requestCount atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var request testManagementTestsRequest
		require.NoError(t, json.Unmarshal(body, &request))
		require.Empty(t, request.Data.Attributes.CommitSha)

		w.Header().Set(HeaderContentType, ContentTypeJSON)
		response := testManagementTestsResponse{}
		response.Data.Type = testManagementTestsRequestType
		response.Data.Attributes.Modules = map[string]TestManagementTestsResponseDataSuites{
			"module": {
				Suites: map[string]TestManagementTestsResponseDataTests{
					"suite": {
						Tests: map[string]TestManagementTestsResponseDataTestProperties{
							"test": {Properties: TestManagementTestsResponseDataTestPropertiesAttributes{AttemptToFix: true}},
						},
					},
				},
			},
		}
		require.NoError(t, json.NewEncoder(w).Encode(response))
	}))
	defer server.Close()
	setupReadCacheEndpointEnv(t, server.URL, root, &now)

	first := NewClient().(*client)
	first.commitSha = ""
	response, err := first.GetTestManagementTests()
	require.NoError(t, err)
	require.True(t, response.Modules["module"].Suites["suite"].Tests["test"].Properties.AttemptToFix)

	second := NewClient().(*client)
	second.commitSha = ""
	response, err = second.GetTestManagementTests()
	require.NoError(t, err)
	require.True(t, response.Modules["module"].Suites["suite"].Tests["test"].Properties.AttemptToFix)
	require.Equal(t, int64(1), requestCount.Load())
}

func setReadCacheHooksForTest(t *testing.T, root string, now *time.Time, pid int, parentPID int) {
	t.Helper()
	SetReadCacheHooksForTesting(
		root,
		func() time.Time { return *now },
		func() int { return pid },
		func() int { return parentPID },
		func(duration time.Duration) { *now = now.Add(duration) },
	)
	t.Cleanup(ResetReadCacheHooksForTesting)
}

func setupReadCacheEndpointEnv(t *testing.T, serverURL string, root string, now *time.Time) {
	t.Helper()

	origEnv := saveEnv()
	path := env.Get("PATH")
	t.Cleanup(func() { restoreEnv(origEnv) })
	setCiVisibilityEnv(path, serverURL)
	setReadCacheHooksForTest(t, root, now, 111, 222)
}

func newReadCacheTestClient(ciTags map[string]string) *client {
	return &client{
		agentless:     true,
		baseURL:       "https://api.example.com/path?token=secret",
		environment:   "test",
		serviceName:   "service",
		repositoryURL: "https://github.com/DataDog/dd-trace-go.git",
		commitSha:     "1234567890abcdef1234567890abcdef12345678",
		branchName:    "main",
		testConfigurations: testConfigurations{
			OsPlatform: "linux",
			Custom:     map[string]string{},
		},
		readCacheScopeIdentity: newReadCacheScopeIdentity(ciTags),
	}
}

func readCacheTestKeyAndPaths(t *testing.T, c *client, endpoint string, semanticRequest any) (string, readCachePaths) {
	t.Helper()

	requestHash, err := readCacheHashJSON(semanticRequest)
	require.NoError(t, err)
	endpointScope := readCacheEndpointScope{Endpoint: endpoint, EndpointVersion: readCacheEndpointVersion, RequestHash: requestHash}
	cacheKey, err := readCacheKey(c.readCacheBaseScope(), endpointScope)
	require.NoError(t, err)
	paths, err := readCachePathsForKey(cacheKey)
	require.NoError(t, err)
	return cacheKey, paths
}

func writeReadCacheTestEntry[T any](t *testing.T, path string, entry readCacheEntry[T]) {
	t.Helper()

	raw, err := json.Marshal(entry)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0o600))
}

func readCacheJSONFiles(t *testing.T, root string) []string {
	t.Helper()

	matches, err := filepath.Glob(filepath.Join(root, "dd-trace-go", "civisibility-read-cache", "*.json"))
	require.NoError(t, err)
	return matches
}

func assertLowerHex(t *testing.T, value string) {
	t.Helper()

	require.Regexp(t, regexp.MustCompile(`^[0-9a-f]{64}$`), value)
}

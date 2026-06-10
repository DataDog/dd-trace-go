// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package net

import (
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
)

// readCacheBenchmarkRequest is a stable semantic request that resembles the
// CI Visibility read endpoints cached by readThroughShortLivedCache.
type readCacheBenchmarkRequest struct {
	Endpoint           string             `json:"endpoint"`
	Service            string             `json:"service"`
	Env                string             `json:"env"`
	RepositoryURL      string             `json:"repository_url"`
	CommitSHA          string             `json:"commit_sha"`
	TestConfigurations testConfigurations `json:"test_configurations"`
}

// readCacheBenchmarkResponse is intentionally structured like a CI Visibility
// read payload so benchmark runs exercise JSON encoding and decoding work.
type readCacheBenchmarkResponse struct {
	CorrelationID string                         `json:"correlation_id"`
	Tests         map[string]map[string][]string `json:"tests"`
	Features      []string                       `json:"features"`
}

// readCacheBenchmarkSink keeps benchmark results observable to the compiler.
var readCacheBenchmarkSink readCacheBenchmarkResponse

// BenchmarkReadThroughShortLivedCache measures the cache wrapper around the
// paths that matter for Go package test process startup: misses that do not
// persist data, cold cache population, and hot cache reuse.
func BenchmarkReadThroughShortLivedCache(b *testing.B) {
	b.Run("MissNoWrite", benchmarkReadThroughShortLivedCacheMissNoWrite)
	b.Run("MissWrite", benchmarkReadThroughShortLivedCacheMissWrite)
	b.Run("Hit", benchmarkReadThroughShortLivedCacheHit)
}

// benchmarkReadThroughShortLivedCacheMissNoWrite measures the miss path when
// the endpoint returns a successful response that should not be cached.
func benchmarkReadThroughShortLivedCacheMissNoWrite(b *testing.B) {
	now := time.Unix(1_700_000_000, 0)
	setReadCacheHooksForBenchmark(b, b.TempDir(), &now, 111, 222)

	c := newReadCacheBenchmarkClient()
	request := newReadCacheBenchmarkRequest(c)
	response := newReadCacheBenchmarkResponse()
	liveCalls := 0
	live := func() (readCacheLiveResult[readCacheBenchmarkResponse], error) {
		liveCalls++
		return readCacheLiveResult[readCacheBenchmarkResponse]{
			Value:     response,
			Cacheable: false,
		}, nil
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		value, err := readThroughShortLivedCache(c, readCacheEndpointKnownTests, request, live, nil)
		if err != nil {
			b.Fatal(err)
		}
		readCacheBenchmarkSink = value
	}
	b.ReportMetric(float64(liveCalls)/float64(b.N), "live_calls/op")
}

// benchmarkReadThroughShortLivedCacheMissWrite measures the cold-cache path
// that acquires ownership, calls the live endpoint, and writes the cache entry.
func benchmarkReadThroughShortLivedCacheMissWrite(b *testing.B) {
	now := time.Unix(1_700_000_000, 0)
	setReadCacheHooksForBenchmark(b, b.TempDir(), &now, 111, 222)

	c := newReadCacheBenchmarkClient()
	request := newReadCacheBenchmarkRequest(c)
	_, paths := readCacheBenchmarkKeyAndPaths(b, c, readCacheEndpointKnownTests, request)
	response := newReadCacheBenchmarkResponse()
	liveCalls := 0
	live := func() (readCacheLiveResult[readCacheBenchmarkResponse], error) {
		liveCalls++
		return readCacheLiveResult[readCacheBenchmarkResponse]{
			Value:     response,
			Cacheable: true,
		}, nil
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		removeReadCacheFile(paths.CacheFile)
		removeReadCacheFile(paths.LockFile)
		b.StartTimer()

		value, err := readThroughShortLivedCache(c, readCacheEndpointKnownTests, request, live, nil)
		if err != nil {
			b.Fatal(err)
		}
		readCacheBenchmarkSink = value
	}
	b.StopTimer()
	if liveCalls != b.N {
		b.Fatalf("expected %d live calls, got %d", b.N, liveCalls)
	}
	b.ReportMetric(float64(liveCalls)/float64(b.N), "live_calls/op")
}

// benchmarkReadThroughShortLivedCacheHit measures repeated reuse of an existing
// cache entry and fails if the live endpoint is called after warmup.
func benchmarkReadThroughShortLivedCacheHit(b *testing.B) {
	now := time.Unix(1_700_000_000, 0)
	setReadCacheHooksForBenchmark(b, b.TempDir(), &now, 111, 222)

	c := newReadCacheBenchmarkClient()
	request := newReadCacheBenchmarkRequest(c)
	response := newReadCacheBenchmarkResponse()
	liveCalls := 0
	live := func() (readCacheLiveResult[readCacheBenchmarkResponse], error) {
		liveCalls++
		return readCacheLiveResult[readCacheBenchmarkResponse]{
			Value:     response,
			Cacheable: true,
		}, nil
	}

	value, err := readThroughShortLivedCache(c, readCacheEndpointKnownTests, request, live, nil)
	if err != nil {
		b.Fatal(err)
	}
	if liveCalls != 1 {
		b.Fatalf("expected warmup to call live once, got %d", liveCalls)
	}
	readCacheBenchmarkSink = value

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		value, err := readThroughShortLivedCache(c, readCacheEndpointKnownTests, request, live, nil)
		if err != nil {
			b.Fatal(err)
		}
		readCacheBenchmarkSink = value
	}
	b.StopTimer()
	if liveCalls != 1 {
		b.Fatalf("cache hit path called live %d extra times", liveCalls-1)
	}
	b.ReportMetric(0, "live_calls/op")
}

// setReadCacheHooksForBenchmark pins process hooks so benchmarks use a stable
// temporary on-disk cache root without depending on the host process.
func setReadCacheHooksForBenchmark(b *testing.B, root string, now *time.Time, pid int, parentPID int) {
	b.Helper()
	SetReadCacheHooksForTesting(
		root,
		func() time.Time { return *now },
		func() int { return pid },
		func() int { return parentPID },
		func(duration time.Duration) { *now = now.Add(duration) },
	)
	b.Cleanup(ResetReadCacheHooksForTesting)
}

// newReadCacheBenchmarkClient builds a representative client scope without
// touching global process environment.
func newReadCacheBenchmarkClient() *client {
	return &client{
		agentless:     true,
		baseURL:       "https://api.example.com/api?token=secret",
		environment:   "benchmark",
		serviceName:   "benchmark-service",
		repositoryURL: "https://github.com/DataDog/dd-trace-go.git",
		commitSha:     "1234567890abcdef1234567890abcdef12345678",
		branchName:    "main",
		testConfigurations: testConfigurations{
			OsPlatform: "linux",
			Custom: map[string]string{
				"arch": "amd64",
			},
		},
		readCacheScopeIdentity: newReadCacheScopeIdentity(map[string]string{
			constants.CIProviderName: "github",
			constants.CIPipelineID:   "benchmark-pipeline",
			constants.CIJobID:        "benchmark-job",
		}),
	}
}

// newReadCacheBenchmarkRequest returns the semantic request hashed into the
// benchmark cache key.
func newReadCacheBenchmarkRequest(c *client) readCacheBenchmarkRequest {
	return readCacheBenchmarkRequest{
		Endpoint:           readCacheEndpointKnownTests,
		Service:            c.serviceName,
		Env:                c.environment,
		RepositoryURL:      c.repositoryURL,
		CommitSHA:          c.commitSha,
		TestConfigurations: c.testConfigurations,
	}
}

// newReadCacheBenchmarkResponse returns a small but nested response payload.
func newReadCacheBenchmarkResponse() readCacheBenchmarkResponse {
	return readCacheBenchmarkResponse{
		CorrelationID: "benchmark-correlation-id",
		Features: []string{
			readCacheEndpointSettings,
			readCacheEndpointKnownTests,
			readCacheEndpointSkippableTests,
			readCacheEndpointTestManagementTests,
		},
		Tests: map[string]map[string][]string{
			"module-a": {
				"suite-a": {"TestOne", "TestTwo", "TestThree"},
				"suite-b": {"TestFour", "TestFive", "TestSix"},
			},
			"module-b": {
				"suite-c": {"TestSeven", "TestEight", "TestNine"},
				"suite-d": {"TestTen", "TestEleven", "TestTwelve"},
			},
		},
	}
}

// readCacheBenchmarkKeyAndPaths computes the cache file paths for benchmark
// setup without using testing.T-only helpers.
func readCacheBenchmarkKeyAndPaths(b *testing.B, c *client, endpoint string, semanticRequest any) (string, readCachePaths) {
	b.Helper()

	requestHash, err := readCacheHashJSON(semanticRequest)
	if err != nil {
		b.Fatal(err)
	}
	endpointScope := readCacheEndpointScope{Endpoint: endpoint, EndpointVersion: readCacheEndpointVersion, RequestHash: requestHash}
	cacheKey, err := readCacheKey(c.readCacheBaseScope(), endpointScope)
	if err != nil {
		b.Fatal(err)
	}
	paths, err := readCachePathsForKey(cacheKey)
	if err != nil {
		b.Fatal(err)
	}
	return cacheKey, paths
}

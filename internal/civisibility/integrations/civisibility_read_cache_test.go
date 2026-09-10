// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package integrations

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/bazel"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
	civisibilitynet "github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
	"github.com/DataDog/dd-trace-go/v2/internal/locking"
)

func TestReadCachePreservesAdditionalFeatureInitialization(t *testing.T) {
	resetCIVisibilityStateForTesting()
	t.Cleanup(resetCIVisibilityStateForTesting)

	var counts readCacheBackendCounts
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(civisibilitynet.HeaderContentType, civisibilitynet.ContentTypeJSON)
		counts.increment(r.URL.Path)
		switch r.URL.Path {
		case "/api/v2/libraries/tests/services/setting":
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"id":   "settings-id",
					"type": "ci_app_test_service_libraries_settings",
					"attributes": map[string]any{
						"code_coverage":               true,
						"flaky_test_retries_enabled":  false,
						"itr_enabled":                 true,
						"require_git":                 false,
						"tests_skipping":              true,
						"known_tests_enabled":         true,
						"impacted_tests_enabled":      false,
						"subtest_features_enabled":    true,
						"early_flake_detection":       map[string]any{"enabled": false, "slow_test_retries": map[string]int{}},
						"test_management":             map[string]any{"enabled": true, "attempt_to_fix_retries": 3},
						"test_management_tests_count": 1,
					},
				},
			}))
		case "/api/v2/ci/libraries/tests":
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"id":   "known-id",
					"type": "ci_app_libraries_tests_request",
					"attributes": map[string]any{
						"tests": map[string]any{
							"module": map[string]any{
								"suite": []string{"TestKnown"},
							},
						},
					},
				},
			}))
		case "/api/v2/ci/tests/skippable":
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"meta": map[string]any{"correlation_id": "correlation-id"},
				"data": []map[string]any{
					{
						"id":   "skippable-id",
						"type": "test",
						"attributes": map[string]any{
							"suite":          "suite",
							"name":           "TestSkippable",
							"parameters":     "",
							"configurations": map[string]any{},
						},
					},
				},
			}))
		case "/api/v2/test/libraries/test-management/tests":
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"id":   "test-management-id",
					"type": "ci_app_libraries_tests_request",
					"attributes": map[string]any{
						"modules": map[string]any{
							"module": map[string]any{
								"suites": map[string]any{
									"suite": map[string]any{
										"tests": map[string]any{
											"TestManaged": map[string]any{
												"properties": map[string]any{
													"attempt_to_fix": true,
													"disabled":       false,
													"quarantined":    false,
												},
											},
										},
									},
								},
							},
						},
					},
				},
			}))
		default:
			t.Fatalf("unexpected backend path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	t.Setenv(constants.CIVisibilityAgentlessEnabledEnvironmentVariable, "true")
	t.Setenv(constants.APIKeyEnvironmentVariable, "test_api_key")
	t.Setenv(constants.CIVisibilityAgentlessURLEnvironmentVariable, server.URL)
	t.Setenv("DD_GIT_REPOSITORY_URL", "https://github.com/DataDog/dd-trace-go.git")
	t.Setenv("DD_GIT_COMMIT_SHA", "1234567890abcdef1234567890abcdef12345678")
	t.Setenv("DD_GIT_BRANCH", "refs/heads/main")
	t.Setenv(constants.CIVisibilityFlakyRetryEnabledEnvironmentVariable, "false")
	t.Setenv(constants.CIVisibilityImpactedTestsDetectionEnabled, "false")
	utils.ResetCITags()
	bazel.ResetForTesting()

	cacheRoot := t.TempDir()
	now := time.Unix(1_700_000_000, 0)
	civisibilitynet.SetReadCacheHooksForTesting(
		cacheRoot,
		func() time.Time { return now },
		func() int { return 111 },
		func() int { return 222 },
		func(duration time.Duration) { now = now.Add(duration) },
	)
	t.Cleanup(civisibilitynet.ResetReadCacheHooksForTesting)

	ensureSettingsInitialization("read-cache-service")
	ensureAdditionalFeaturesInitialization("read-cache-service")
	firstCounts := counts.snapshot()
	firstSettings := ciVisibilitySettings
	firstKnownTests := ciVisibilityKnownTests
	firstSkippables := ciVisibilitySkippables
	firstTestManagement := ciVisibilityTestManagementTests
	firstTags := copyReadCacheTags(utils.GetCITags())

	require.Equal(t, readCacheBackendCountsSnapshot{settings: 1, knownTests: 1, skippableTests: 1, testManagementTests: 1}, firstCounts)

	resetCIVisibilityStateForTesting()
	civisibilitynet.SetReadCacheHooksForTesting(
		cacheRoot,
		func() time.Time { return now },
		func() int { return 111 },
		func() int { return 222 },
		func(duration time.Duration) { now = now.Add(duration) },
	)

	ensureSettingsInitialization("read-cache-service")
	ensureAdditionalFeaturesInitialization("read-cache-service")
	secondCounts := counts.snapshot()

	require.Equal(t, firstCounts, secondCounts)
	require.Equal(t, firstSettings, ciVisibilitySettings)
	require.Equal(t, firstKnownTests, ciVisibilityKnownTests)
	require.Equal(t, firstSkippables, ciVisibilitySkippables)
	require.Equal(t, firstTestManagement, ciVisibilityTestManagementTests)
	require.Equal(t, firstTags, copyReadCacheTags(utils.GetCITags()))
}

type readCacheBackendCounts struct {
	mu                  locking.Mutex
	settings            int
	knownTests          int
	skippableTests      int
	testManagementTests int
}

type readCacheBackendCountsSnapshot struct {
	settings            int
	knownTests          int
	skippableTests      int
	testManagementTests int
}

func (c *readCacheBackendCounts) increment(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch path {
	case "/api/v2/libraries/tests/services/setting":
		c.settings++
	case "/api/v2/ci/libraries/tests":
		c.knownTests++
	case "/api/v2/ci/tests/skippable":
		c.skippableTests++
	case "/api/v2/test/libraries/test-management/tests":
		c.testManagementTests++
	}
}

func (c *readCacheBackendCounts) snapshot() readCacheBackendCountsSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	return readCacheBackendCountsSnapshot{
		settings:            c.settings,
		knownTests:          c.knownTests,
		skippableTests:      c.skippableTests,
		testManagementTests: c.testManagementTests,
	}
}

func copyReadCacheTags(tags map[string]string) map[string]string {
	copied := make(map[string]string, len(tags))
	maps.Copy(copied, tags)
	return copied
}

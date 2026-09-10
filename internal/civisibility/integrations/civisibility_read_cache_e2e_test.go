// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package integrations

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/bazel"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	civisibilitynet "github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
)

func TestReadCacheSharesBootstrapAcrossGoTestPackages(t *testing.T) {
	resetCIVisibilityStateForTesting()
	t.Cleanup(resetCIVisibilityStateForTesting)

	var counts readCacheBackendCounts
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(civisibilitynet.HeaderContentType, civisibilitynet.ContentTypeJSON)
		counts.increment(r.URL.Path)
		writeReadCacheE2EResponse(t, w, r.URL.Path)
	}))
	defer server.Close()

	moduleDir := writeReadCacheE2EModule(t, 2)
	homeDir := t.TempDir()
	outputDir := t.TempDir()
	moduleCacheDir := filepath.Join(t.TempDir(), "go-mod")
	t.Cleanup(func() {
		removeReadCacheE2EModuleCache(t, moduleCacheDir)
	})

	cmd := exec.Command("go", "test", "-mod=mod", "-count=1", "-p=2", "./...")
	cmd.Dir = moduleDir
	cmd.Env = readCacheE2EEnv(os.Environ(), []readCacheE2EEnvVar{
		{Name: "HOME", Value: homeDir},
		{Name: "XDG_CACHE_HOME", Value: filepath.Join(homeDir, "xdg-cache")},
		{Name: "GOCACHE", Value: filepath.Join(t.TempDir(), "go-build")},
		{Name: "GOMODCACHE", Value: moduleCacheDir},
		{Name: "GOWORK", Value: "off"},
		{Name: "GOFLAGS", Value: ""},
		{Name: constants.CIVisibilityAgentlessEnabledEnvironmentVariable, Value: "true"},
		{Name: constants.APIKeyEnvironmentVariable, Value: "test_api_key"},
		{Name: constants.CIVisibilityAgentlessURLEnvironmentVariable, Value: server.URL},
		{Name: constants.CIVisibilityFlakyRetryEnabledEnvironmentVariable, Value: "false"},
		{Name: constants.CIVisibilityImpactedTestsDetectionEnabled, Value: "false"},
		{Name: "DD_SERVICE", Value: "read-cache-e2e-service"},
		{Name: "DD_GIT_REPOSITORY_URL", Value: "https://github.com/DataDog/dd-trace-go.git"},
		{Name: "DD_GIT_COMMIT_SHA", Value: "1234567890abcdef1234567890abcdef12345678"},
		{Name: "DD_GIT_BRANCH", Value: "refs/heads/main"},
		{Name: "DD_CIVISIBILITY_LOGS_ENABLED", Value: "false"},
		{Name: bazel.PayloadsInFilesEnv, Value: "true"},
		{Name: bazel.UndeclaredOutputsDirEnv, Value: outputDir},
	})

	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	require.NoError(t, err, "go test ./... failed:\n%s", output.String())

	require.Equal(t, readCacheBackendCountsSnapshot{
		settings:            1,
		knownTests:          1,
		skippableTests:      1,
		testManagementTests: 1,
	}, counts.snapshot(), "multi-package go test output:\n%s", output.String())
}

// writeReadCacheE2EResponse serves deterministic CI Visibility read responses for the subprocess module.
func writeReadCacheE2EResponse(t *testing.T, w http.ResponseWriter, path string) {
	t.Helper()

	switch path {
	case "/api/v2/libraries/tests/services/setting":
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"id":   "settings-id",
				"type": "ci_app_test_service_libraries_settings",
				"attributes": map[string]any{
					"code_coverage":               false,
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
							"suite": []string{"TestReadCacheE2EBootstrap"},
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
						"name":           "TestReadCacheE2EBootstrap",
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
										"TestReadCacheE2EBootstrap": map[string]any{
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
		t.Fatalf("unexpected backend path: %s", path)
	}
}

// writeReadCacheE2EModule creates a temporary multi-package module used by the subprocess E2E test.
func writeReadCacheE2EModule(t *testing.T, packageCount int) string {
	t.Helper()

	packageDir, err := os.Getwd()
	require.NoError(t, err)
	repoRoot := filepath.Clean(filepath.Join(packageDir, "..", "..", ".."))
	require.FileExists(t, filepath.Join(repoRoot, "go.mod"))

	moduleDir := t.TempDir()
	goMod := fmt.Sprintf(`module github.com/DataDog/dd-trace-go/v2/internal/civisibility/readcachee2e

go 1.25.0

require github.com/DataDog/dd-trace-go/v2 v2.0.0

replace github.com/DataDog/dd-trace-go/v2 => %s
`, filepath.ToSlash(repoRoot))
	require.NoError(t, os.WriteFile(filepath.Join(moduleDir, "go.mod"), []byte(goMod), 0o600))

	for i := range packageCount {
		pkgDir := filepath.Join(moduleDir, fmt.Sprintf("pkg%d", i))
		require.NoError(t, os.MkdirAll(pkgDir, 0o700))
		source := fmt.Sprintf(readCacheE2EPackageTemplate, i)
		require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "read_cache_e2e_test.go"), []byte(source), 0o600))
	}

	return moduleDir
}

// removeReadCacheE2EModuleCache makes Go's read-only module cache removable before test cleanup.
func removeReadCacheE2EModuleCache(t *testing.T, moduleCacheDir string) {
	t.Helper()

	err := filepath.WalkDir(moduleCacheDir, func(path string, dirEntry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if dirEntry.IsDir() {
			return os.Chmod(path, 0o700)
		}
		return os.Chmod(path, 0o600)
	})
	if err != nil && !os.IsNotExist(err) {
		require.NoError(t, err)
	}
	require.NoError(t, os.RemoveAll(moduleCacheDir))
}

// readCacheE2EPackageTemplate is the generated package test source used by the temporary module.
const readCacheE2EPackageTemplate = `package pkg%d

import (
	"os"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
)

func TestMain(m *testing.M) {
	integrations.InitializeCIVisibilityMock()
	code := m.Run()
	integrations.ExitCiVisibility()
	os.Exit(code)
}

func TestReadCacheE2EBootstrap(t *testing.T) {
	settings := integrations.GetSettings()
	if settings == nil {
		t.Fatal("settings were not loaded")
	}
	if !settings.KnownTestsEnabled || !settings.TestsSkipping || !settings.TestManagement.Enabled {
		t.Fatalf("settings did not enable expected features: known=%%t skipping=%%t management=%%t", settings.KnownTestsEnabled, settings.TestsSkipping, settings.TestManagement.Enabled)
	}
	if known := integrations.GetKnownTests(); known == nil || len(known.Tests) == 0 {
		t.Fatalf("known tests were not loaded: %%#v", known)
	}
	if skippable := integrations.GetSkippableTests(); len(skippable) == 0 {
		t.Fatalf("skippable tests were not loaded: %%#v", skippable)
	}
	if managed := integrations.GetTestManagementTestsData(); managed == nil || len(managed.Modules) == 0 {
		t.Fatalf("test management data was not loaded: %%#v", managed)
	}
}
`

// readCacheE2EEnvVar stores one environment assignment for the subprocess module.
type readCacheE2EEnvVar struct {
	Name  string
	Value string
}

// readCacheE2EEnv applies deterministic subprocess environment overrides and removes CI Visibility leakage.
func readCacheE2EEnv(base []string, overrides []readCacheE2EEnvVar) []string {
	overrideNames := make(map[string]struct{}, len(overrides))
	for _, override := range overrides {
		overrideNames[override.Name] = struct{}{}
	}

	env := make([]string, 0, len(base)+len(overrides))
	for _, item := range base {
		name, _, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		if _, overridden := overrideNames[name]; overridden {
			continue
		}
		if strings.HasPrefix(name, "DD_") || strings.HasPrefix(name, "OTEL_") {
			continue
		}
		env = append(env, item)
	}
	for _, override := range overrides {
		env = append(env, override.Name+"="+override.Value)
	}
	return env
}

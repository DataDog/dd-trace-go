// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package utils

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	serviceFromCodeOwnersEnv       = constants.CIVisibilityServiceFromCodeOwnersEnvironmentVariable
	serviceFromCodeOwnersFormatEnv = constants.CIVisibilityServiceFromCodeOwnersFormatEnvironmentVariable
)

// useCodeOwnersServiceTest isolates the CODEOWNERS and service caches and runs
// the test from the package directory, as go test runs a test binary.
func useCodeOwnersServiceTest(t *testing.T, workspace, packageDirectory string) {
	t.Helper()
	resetCodeOwnersTestState(t, workspace)
	ResetServiceFromCodeOwnersForTesting()
	t.Cleanup(ResetServiceFromCodeOwnersForTesting)
	require.NoError(t, os.MkdirAll(packageDirectory, 0o755))
	t.Chdir(packageDirectory)
}

func TestCodeOwnersPackageService(t *testing.T) {
	for _, tc := range []struct {
		name, enabled, service, format, rules, directory, want string
	}{
		{name: "default disabled", rules: "* @org/team", directory: "pkg"},
		{name: "disabled", enabled: "false", rules: "* @org/team", directory: "pkg"},
		{name: "explicit service", enabled: "true", service: "explicit", rules: "* @org/team", directory: "pkg"},
		{name: "default format", enabled: "true", rules: "* @org/team", directory: "pkg", want: "service-team"},
		{name: "custom format", enabled: "true", format: "dd-go-$(owner)", rules: "* @ddoghq/ci-app", directory: "pkg", want: "dd-go-ci-app"},
		{name: "upstream organization", enabled: "true", format: "dd-go-$(owner)", rules: "* @DataDog/ci-app", directory: "pkg", want: "dd-go-ci-app"},
		{name: "username", enabled: "true", rules: "* @user", directory: "pkg", want: "service-user"},
		{name: "email", enabled: "true", rules: "* ci@example.com", directory: "pkg", want: "service-ci@example.com"},
		{name: "email with slash", enabled: "true", rules: "* ci/tests@example.com", directory: "pkg", want: "service-ci/tests@example.com"},
		{name: "first owner", enabled: "true", rules: "* @org/first @org/second", directory: "pkg", want: "service-first"},
		{name: "last directory rule", enabled: "true", rules: "* @org/fallback\n/pkg/ @org/specific", directory: "pkg", want: "service-specific"},
		{name: "exact directory rule", enabled: "true", rules: "* @org/fallback\n/pkg @org/specific", directory: "pkg", want: "service-specific"},
		{name: "literal parent inheritance", enabled: "true", rules: "/pkg @org/parent", directory: "pkg/nested", want: "service-parent"},
		{name: "interior wildcard", enabled: "true", rules: "/pkg/*/tests/ @org/nested", directory: "pkg/team/tests/deep", want: "service-nested"},
		{name: "double star", enabled: "true", rules: "/pkg/**/mobile* @org/mobile", directory: "pkg/a/b/mobile-tests/deep", want: "service-mobile"},
		{name: "double star zero segments", enabled: "true", rules: "/pkg/**/mobile* @org/mobile", directory: "pkg/mobile-tests", want: "service-mobile"},
		{name: "unanchored directory", enabled: "true", rules: "tests/ @org/tests", directory: "pkg/tests/nested", want: "service-tests"},
		{name: "root anchored directory", enabled: "true", rules: "tests/ @org/tests\n/tests/ @org/root", directory: "pkg/tests/nested", want: "service-tests"},
		{name: "direct wildcard excludes descendants", enabled: "true", rules: "* @org/fallback\n/pkg/* @org/children", directory: "pkg/child/deep", want: "service-fallback"},
		{name: "direct children wildcard", enabled: "true", rules: "/pkg/* @org/children", directory: "pkg/child", want: "service-children"},
		{name: "owner removal", enabled: "true", rules: "* @org/team\n/pkg/", directory: "pkg", want: "service-not-owned"},
		{name: "no matching rule", enabled: "true", rules: "/another/ @org/team", directory: "pkg", want: "service-not-owned"},
		{name: "root package", enabled: "true", rules: "/* @org/root", directory: ".", want: "service-root"},
		{name: "repeated placeholder", enabled: "true", format: "$(owner)-$(owner)", rules: "* @org/team", directory: "pkg", want: "team-team"},
		{name: "literal format", enabled: "true", format: "literal-service", rules: "* @org/team", directory: "pkg", want: "literal-service"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			useCodeOwnersServiceTest(t, root, filepath.Join(root, tc.directory))
			t.Setenv(serviceFromCodeOwnersEnv, tc.enabled)
			if tc.enabled == "" {
				require.NoError(t, os.Unsetenv(serviceFromCodeOwnersEnv))
			}
			t.Setenv(serviceFromCodeOwnersFormatEnv, tc.format)
			t.Setenv("DD_SERVICE", tc.service)
			writeCodeOwnersFile(t, filepath.Join(root, "CODEOWNERS"), tc.rules+"\n")

			assert.Equal(t, tc.want, ServiceFromCodeOwners())
			assert.Equal(t, tc.service, os.Getenv("DD_SERVICE"), "DD_SERVICE was changed")
			if tc.enabled != "true" || tc.service != "" {
				assert.False(t, codeownersLookupDone, "disabled or explicit service performed CODEOWNERS discovery")
			}
		})
	}
}

func TestCodeOwnersServiceMissingInputs(t *testing.T) {
	for _, tc := range []struct {
		name, content, want                string
		missing, outside, workspaceMissing bool
	}{
		{name: "missing file", missing: true},
		{name: "rule without owners", content: strings.Repeat("x", 70*1024), want: "service-not-owned"},
		{name: "outside workspace", content: "* @org/team\n", outside: true},
		{name: "missing workspace", content: "* @org/team\n", workspaceMissing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			directory := filepath.Join(root, "pkg")
			if tc.outside {
				directory = t.TempDir()
			}
			useCodeOwnersServiceTest(t, root, directory)
			t.Setenv(serviceFromCodeOwnersEnv, "true")
			t.Setenv("DD_SERVICE", "")
			if !tc.missing {
				writeCodeOwnersFile(t, filepath.Join(root, "CODEOWNERS"), tc.content)
			}
			if tc.workspaceMissing {
				originalCiTags = map[string]string{}
			}
			assert.Equal(t, tc.want, ServiceFromCodeOwners(), "fallback overridden")
		})
	}
}

func TestCodeOwnersServiceCacheConcurrentReaders(t *testing.T) {
	root := t.TempDir()
	useCodeOwnersServiceTest(t, root, filepath.Join(root, "pkg"))
	t.Setenv(serviceFromCodeOwnersEnv, "true")
	t.Setenv(serviceFromCodeOwnersFormatEnv, "dd-go-$(owner)")
	t.Setenv("DD_SERVICE", "")
	writeCodeOwnersFile(t, filepath.Join(root, "CODEOWNERS"), "/pkg/ @org/team\n")
	var workers sync.WaitGroup
	for range 32 {
		workers.Go(func() {
			assert.Equal(t, "dd-go-team", ServiceFromCodeOwners())
		})
	}
	workers.Wait()
	writeCodeOwnersFile(t, filepath.Join(root, "CODEOWNERS"), "/pkg/ @org/changed\n")
	assert.Equal(t, "dd-go-team", ServiceFromCodeOwners(), "process identity changed")
	t.Setenv("DD_SERVICE", "explicit-later")
	assert.Empty(t, ServiceFromCodeOwners(), "cache overrode explicit service")
}

// The directory registered at start-up selects the package, even when TestMain
// changes directories before the tests run.
func TestCodeOwnersServiceUsesRegisteredDirectory(t *testing.T) {
	root := t.TempDir()
	useCodeOwnersServiceTest(t, root, filepath.Join(root, "shared"))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "payments"), 0o755))
	RegisterTestPackageDirectory(filepath.Join(root, "payments"))
	RegisterTestPackageDirectory(filepath.Join(root, "shared"))
	t.Setenv(serviceFromCodeOwnersEnv, "true")
	t.Setenv(serviceFromCodeOwnersFormatEnv, "")
	t.Setenv("DD_SERVICE", "")
	writeCodeOwnersFile(t, filepath.Join(root, "CODEOWNERS"), "/payments/ @org/payments\n/shared/ @org/shared\n")

	assert.Equal(t, "service-payments", ServiceFromCodeOwners())
}

// A workspace path through a symlink still contains the package directory.
func TestCodeOwnersPackageDirectorySymlink(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.Symlink(root, workspace); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	for _, directory := range []string{filepath.Join(root, "pkg"), filepath.Join(workspace, "pkg")} {
		t.Run(directory, func(t *testing.T) {
			useCodeOwnersServiceTest(t, workspace, directory)
			t.Setenv(serviceFromCodeOwnersEnv, "true")
			t.Setenv(serviceFromCodeOwnersFormatEnv, "")
			t.Setenv("DD_SERVICE", "")
			writeCodeOwnersFile(t, filepath.Join(root, "CODEOWNERS"), "/pkg/ @org/team\n")
			assert.Equal(t, "service-team", ServiceFromCodeOwners())
		})
	}
}

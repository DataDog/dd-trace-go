// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package utils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/codeownership"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resetCodeOwnersTestState(t *testing.T, workspaceDir string) {
	t.Helper()

	ResetCodeOwnersForTesting()
	ResetCITags()
	originalCiTags = map[string]string{constants.CIWorkspacePath: workspaceDir}
	t.Cleanup(func() {
		ResetCodeOwnersForTesting()
		ResetCITags()
	})
}

func writeCodeOwnersFile(t *testing.T, path string, content string) {
	t.Helper()

	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func TestNilCodeOwnersDoesNotMatch(t *testing.T) {
	var codeOwners *codeownership.Resolver
	match, ok := codeOwners.Match("/source.go")
	assert.Nil(t, match)
	assert.False(t, ok)
}

func TestGetCodeOwnersCachesMissingDiscovery(t *testing.T) {
	workspaceDir := t.TempDir()
	workingDir := t.TempDir()
	t.Chdir(workingDir)
	resetCodeOwnersTestState(t, workspaceDir)

	codeOwners, complete := GetCodeOwnersWithStatus()
	assert.Nil(t, codeOwners)
	assert.True(t, complete)

	writeCodeOwnersFile(t, filepath.Join(workspaceDir, "CODEOWNERS"), "/cached/miss @owner\n")
	assert.Nil(t, GetCodeOwners())

	ResetCodeOwnersForTesting()
	codeOwners = GetCodeOwners()
	require.NotNil(t, codeOwners)

	match, ok := codeOwners.Match("/cached/miss")
	require.True(t, ok)
	assert.Equal(t, "[\"@owner\"]", match.Tag())
}

func TestGetCodeOwnersCachesSuccessfulDiscovery(t *testing.T) {
	workspaceDir := t.TempDir()
	otherWorkspaceDir := t.TempDir()
	workingDir := t.TempDir()
	t.Chdir(workingDir)
	resetCodeOwnersTestState(t, workspaceDir)

	writeCodeOwnersFile(t, filepath.Join(workspaceDir, "CODEOWNERS"), "/cached/success @first\n")
	writeCodeOwnersFile(t, filepath.Join(otherWorkspaceDir, "CODEOWNERS"), "/cached/success @second\n")

	codeOwners := GetCodeOwners()
	require.NotNil(t, codeOwners)

	ResetCITags()
	originalCiTags = map[string]string{constants.CIWorkspacePath: otherWorkspaceDir}

	cachedCodeOwners := GetCodeOwners()
	require.True(t, codeOwners == cachedCodeOwners)
	match, ok := cachedCodeOwners.Match("/cached/success")
	require.True(t, ok)
	assert.Equal(t, "[\"@first\"]", match.Tag())
}

// A binary that runs where its CI workspace does not exist still finds the
// CODEOWNERS file in its current directory.
func TestGetCodeOwnersFallsBackToWorkingDirectory(t *testing.T) {
	workingDir := t.TempDir()
	t.Chdir(workingDir)
	resetCodeOwnersTestState(t, filepath.Join(t.TempDir(), "missing-workspace"))
	writeCodeOwnersFile(t, filepath.Join(workingDir, "CODEOWNERS"), "/fallback @owner\n")

	codeOwners, complete := GetCodeOwnersWithStatus()
	require.NotNil(t, codeOwners)
	require.True(t, complete)
	match, ok := codeOwners.Match("/fallback")
	require.True(t, ok)
	assert.Equal(t, "[\"@owner\"]", match.Tag())
}

// The last-resort file keeps the callers' repository-relative paths, even when
// the current directory is inside a Git checkout.
func TestGetCodeOwnersFallbackKeepsPaths(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o700))
	workingDir := filepath.Join(root, "pkg")
	require.NoError(t, os.Mkdir(workingDir, 0o700))
	t.Chdir(workingDir)
	resetCodeOwnersTestState(t, filepath.Join(t.TempDir(), "missing-workspace"))
	writeCodeOwnersFile(t, filepath.Join(workingDir, "CODEOWNERS"), "* @global\n/pkg/a_test.go @specific\n")

	codeOwners := GetCodeOwners()
	require.NotNil(t, codeOwners)
	match, ok := codeOwners.Match("/pkg/a_test.go")
	require.True(t, ok)
	assert.Equal(t, "@specific", match.FirstOwner())
}

// A read error is retried later; it never selects another candidate file.
func TestGetCodeOwnersRetriesIOFailure(t *testing.T) {
	workspaceDir := t.TempDir()
	t.Chdir(t.TempDir())
	resetCodeOwnersTestState(t, workspaceDir)

	codeOwnersPath := filepath.Join(workspaceDir, "CODEOWNERS")
	if err := os.Symlink("CODEOWNERS", codeOwnersPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	codeOwners, complete := GetCodeOwnersWithStatus()
	assert.Nil(t, codeOwners)
	assert.False(t, complete)

	require.NoError(t, os.Remove(codeOwnersPath))
	writeCodeOwnersFile(t, codeOwnersPath, "/fixed @owner\n")
	codeOwners, complete = GetCodeOwnersWithStatus()
	require.NotNil(t, codeOwners)
	require.True(t, complete)

	match, ok := codeOwners.Match("/fixed")
	require.True(t, ok)
	assert.Equal(t, "[\"@owner\"]", match.Tag())
}

// Each host reads its own first location: GitHub prefers .github/CODEOWNERS,
// GitLab prefers the root file.
func TestGetCodeOwnersUsesProviderLocation(t *testing.T) {
	workspaceDir := t.TempDir()
	t.Chdir(t.TempDir())
	resetCodeOwnersTestState(t, workspaceDir)

	for _, folder := range []string{".github", ".gitlab", "docs"} {
		require.NoError(t, os.MkdirAll(filepath.Join(workspaceDir, folder), 0o700))
	}
	writeCodeOwnersFile(t, filepath.Join(workspaceDir, "CODEOWNERS"), "* @root\n")
	writeCodeOwnersFile(t, filepath.Join(workspaceDir, ".github", "CODEOWNERS"), "* @github\n")
	writeCodeOwnersFile(t, filepath.Join(workspaceDir, ".gitlab", "CODEOWNERS"), "* @gitlab\n")
	writeCodeOwnersFile(t, filepath.Join(workspaceDir, "docs", "CODEOWNERS"), "* @docs\n")

	match, ok := GetCodeOwners().Match("/a.go")
	require.True(t, ok)
	assert.Equal(t, "@github", match.FirstOwner())

	ResetCodeOwnersForTesting()
	AddCITags(constants.CIProviderName, "gitlab")
	match, ok = GetCodeOwners().Match("/a.go")
	require.True(t, ok)
	assert.Equal(t, "@root", match.FirstOwner())
}

// The previous matcher treated these wildcards as literal characters; GitHub,
// and now CI Visibility, match them.
func TestMatchAnchoredFilenamePrefix(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		path    string
		matches bool
	}{
		{"/src/ci_*", "/src/ci_new.go", true},
		{"/src/ci_*", "/src/ci_new_test.go", true},
		{"/src/ci_*", "/src/ci_", true},
		{"/src/ci_*", "/src/ci_group/nested.go", true},
		{"/ci_*", "/ci_root.go", true},
		{"/src/ci_*", "/src/app.go", false},
		{"/src/ci_*", "/src/ci.go", false},
		{"/src/ci_*", "/src/CI_new.go", false},
		{"/src/ci_*", "/src/nested/ci_new.go", false},
		{"/src/ci_*", "/other/src/ci_new.go", false},
		{"/src/ci_*", "/other/ci_new.go", false},
		{"/src/*", "/src/direct.go", true},
		{"/src/*", "/src/nested/direct.go", false},
		{"/src/", "/src/nested/direct.go", true},
		{"*test.go", "/src/nested/direct_test.go", true},
		{"ci_*", "/src/ci_new.go", true},
		{"/src/ci_**", "/src/ci_new.go", true},
		{"/src/ci_?*", "/src/ci_new.go", true},
		{"/src/ci_*.go", "/src/ci_new.go", true},
	} {
		t.Run(tc.pattern+":"+tc.path, func(t *testing.T) {
			owners, err := codeownership.Parse(strings.NewReader(tc.pattern+" @owner\n"), codeownership.GitHub)
			require.NoError(t, err)
			match, ok := owners.Match(tc.path)
			assert.Equal(t, tc.matches, ok)
			if tc.matches {
				assert.Equal(t, []string{"@owner"}, match.Owners())
			}
		})
	}
}

func TestMatchAnchoredFilenamePrefixPrecedence(t *testing.T) {
	for _, tc := range []struct {
		content string
		owner   string
	}{
		{"/src/ @directory\n/src/ci_* @prefix\n/src/ci_exact.go @exact\n", "@exact"},
		{"/src/ @directory\n/src/ci_exact.go @exact\n/src/ci_* @prefix\n", "@prefix"},
		{"/src/ci_* @prefix\n/src/ @directory\n", "@directory"},
	} {
		t.Run(tc.owner, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "CODEOWNERS")
			writeCodeOwnersFile(t, path, tc.content)
			owners, err := codeownership.Load(path, codeownership.GitHub)
			require.NoError(t, err)
			match, ok := owners.Match("/src/ci_exact.go")
			require.True(t, ok)
			assert.Equal(t, []string{tc.owner}, match.Owners())
		})
	}
}

func TestGithubCodeOwners(t *testing.T) {
	cOwners, err := codeownership.Load("testdata/fixtures/codeowners/CODEOWNERS_GITHUB", codeownership.GitHub)
	require.NoError(t, err)

	data := []struct {
		value    string
		expected string
	}{
		{value: "unexistent/path/test.cs", expected: "[\"@global-owner1\",\"@global-owner2\"]"},
		{value: "apps/test.cs", expected: "[\"@octocat\"]"},
		{value: "/example/apps/test.cs", expected: "[\"@octocat\"]"},
		{value: "/docs/test.cs", expected: "[\"@doctocat\"]"},
		// docs/* has an interior slash, so it is rooted: it does not match
		// examples/docs/test.cs, which the previous matcher gave docs@example.com.
		{value: "/examples/docs/test.cs", expected: "[\"@global-owner1\",\"@global-owner2\"]"},
		{value: "/src/vendor/match.go", expected: "[\"docs@example.com\"]"},
		{value: "/examples/docs/inside/test.cs", expected: "[\"@global-owner1\",\"@global-owner2\"]"},
		{value: "/component/path/test.js", expected: "[\"@js-owner\"]"},
		{value: "/mytextbox.txt", expected: "[\"@octo-org/octocats\"]"},
		{value: "/scripts/artifacts/value.js", expected: "[\"@doctocat\",\"@octocat\"]"},
		{value: "/apps/octo/test.cs", expected: "[\"@octocat\"]"},
		{value: "/apps/github", expected: ""},
	}

	for _, item := range data {
		t.Run(strings.ReplaceAll(item.value, "/", "_"), func(t *testing.T) {
			match, ok := cOwners.Match(item.value)
			assert.True(t, ok)
			assert.EqualValues(t, item.expected, match.Tag())
		})
	}
}

func TestGitlabCodeOwners(t *testing.T) {
	cOwners, err := codeownership.Load("testdata/fixtures/codeowners/CODEOWNERS_GITLAB", codeownership.GitLab)
	require.NoError(t, err)

	// Sections combine their owners in declaration order, starting with the
	// rules before the first section header. The previous matcher listed the
	// default section's owners last.
	data := []struct {
		value    string
		expected string
	}{
		{value: "apps/README.md", expected: "[\"@multiple\",\"@code\",\"@owners\",\"@docs\",\"@database\"]"},
		{value: "model/db", expected: "[\"@multiple\",\"@code\",\"@owners\",\"@database\"]"},
		{value: "/config/data.conf", expected: "[\"@config-owner\"]"},
		{value: "/docs/root.md", expected: "[\"@root-docs\"]"},
		{value: "/docs/sub/root.md", expected: "[\"@all-docs\"]"},
		{value: "/src/README", expected: "[\"@group\",\"@group/with-nested/subgroup\"]"},
		{value: "/src/lib/internal.h", expected: "[\"@lib-owner\"]"},
		{value: "src/ee/docs", expected: "[\"@multiple\",\"@code\",\"@owners\",\"@docs\"]"},
	}

	for _, item := range data {
		t.Run(strings.ReplaceAll(item.value, "/", "_"), func(t *testing.T) {
			match, ok := cOwners.Match(item.value)
			assert.True(t, ok)
			assert.EqualValues(t, item.expected, match.Tag())
		})
	}
}

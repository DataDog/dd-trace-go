// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build !windows

package utils

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveSourceFilePath(t *testing.T) {
	workspace := "/ci/workspace"
	tests := []struct {
		name                string
		runtimePath         string
		tags                map[string]string
		mainModulePath      string
		expectedRelative    string
		expectedFilesystem  string
		expectedKnown       bool
		unexpectedRelative  []string
		unexpectedFilePaths []string
	}{
		{
			name:               "empty input",
			expectedRelative:   "",
			expectedFilesystem: "",
			expectedKnown:      false,
		},
		{
			name:               "absolute path inside workspace",
			runtimePath:        "/ci/workspace/services/foo/foo_test.go",
			tags:               map[string]string{constants.CIWorkspacePath: workspace},
			expectedRelative:   "services/foo/foo_test.go",
			expectedFilesystem: "/ci/workspace/services/foo/foo_test.go",
			expectedKnown:      true,
		},
		{
			name:               "absolute path outside workspace",
			runtimePath:        "/tmp/build/foo_test.go",
			tags:               map[string]string{constants.CIWorkspacePath: workspace},
			expectedRelative:   "/tmp/build/foo_test.go",
			expectedFilesystem: "/tmp/build/foo_test.go",
			expectedKnown:      true,
		},
		{
			name:               "repo relative path with workspace",
			runtimePath:        "services/foo/foo_test.go",
			tags:               map[string]string{constants.CIWorkspacePath: workspace},
			expectedRelative:   "services/foo/foo_test.go",
			expectedFilesystem: "/ci/workspace/services/foo/foo_test.go",
			expectedKnown:      true,
		},
		{
			name:               "root file repo relative path with workspace",
			runtimePath:        "foo_test.go",
			tags:               map[string]string{constants.CIWorkspacePath: workspace},
			expectedRelative:   "foo_test.go",
			expectedFilesystem: "/ci/workspace/foo_test.go",
			expectedKnown:      true,
		},
		{
			name:               "repo relative path without workspace",
			runtimePath:        "services/foo/foo_test.go",
			expectedRelative:   "services/foo/foo_test.go",
			expectedFilesystem: "services/foo/foo_test.go",
			expectedKnown:      false,
		},
		{
			name:               "relative path with parent traversal",
			runtimePath:        "../services/foo/foo_test.go",
			tags:               map[string]string{constants.CIWorkspacePath: workspace},
			expectedRelative:   "../services/foo/foo_test.go",
			expectedFilesystem: "../services/foo/foo_test.go",
			expectedKnown:      false,
		},
		{
			name:               "https repository URL",
			runtimePath:        "github.com/myorg/myrepo/services/foo/foo_test.go",
			tags:               map[string]string{constants.CIWorkspacePath: workspace, constants.GitRepositoryURL: "https://github.com/myorg/myrepo.git"},
			expectedRelative:   "services/foo/foo_test.go",
			expectedFilesystem: "/ci/workspace/services/foo/foo_test.go",
			expectedKnown:      true,
		},
		{
			name:               "scp style repository URL",
			runtimePath:        "github.com/myorg/myrepo/services/foo/foo_test.go",
			tags:               map[string]string{constants.CIWorkspacePath: workspace, constants.GitRepositoryURL: "git@github.com:myorg/myrepo.git"},
			expectedRelative:   "services/foo/foo_test.go",
			expectedFilesystem: "/ci/workspace/services/foo/foo_test.go",
			expectedKnown:      true,
		},
		{
			name:               "ssh URL repository URL",
			runtimePath:        "github.com/myorg/myrepo/services/foo/foo_test.go",
			tags:               map[string]string{constants.CIWorkspacePath: workspace, constants.GitRepositoryURL: "ssh://git@github.com/myorg/myrepo.git"},
			expectedRelative:   "services/foo/foo_test.go",
			expectedFilesystem: "/ci/workspace/services/foo/foo_test.go",
			expectedKnown:      true,
		},
		{
			name:               "repository URL with credentials",
			runtimePath:        "github.com/myorg/myrepo/services/foo/foo_test.go",
			tags:               map[string]string{constants.CIWorkspacePath: workspace, constants.GitRepositoryURL: "https://token@github.com/myorg/myrepo.git"},
			expectedRelative:   "services/foo/foo_test.go",
			expectedFilesystem: "/ci/workspace/services/foo/foo_test.go",
			expectedKnown:      true,
		},
		{
			name:               "repository URL with username and password",
			runtimePath:        "github.com/myorg/myrepo/services/foo/foo_test.go",
			tags:               map[string]string{constants.CIWorkspacePath: workspace, constants.GitRepositoryURL: "https://user:password@github.com/myorg/myrepo.git"},
			expectedRelative:   "services/foo/foo_test.go",
			expectedFilesystem: "/ci/workspace/services/foo/foo_test.go",
			expectedKnown:      true,
		},
		{
			name:               "repository URL with port",
			runtimePath:        "github.com/myorg/myrepo/services/foo/foo_test.go",
			tags:               map[string]string{constants.CIWorkspacePath: workspace, constants.GitRepositoryURL: "ssh://git@github.com:2222/myorg/myrepo.git"},
			expectedRelative:   "services/foo/foo_test.go",
			expectedFilesystem: "/ci/workspace/services/foo/foo_test.go",
			expectedKnown:      true,
		},
		{
			name:               "repository URL with trailing slash",
			runtimePath:        "github.com/myorg/myrepo/services/foo/foo_test.go",
			tags:               map[string]string{constants.CIWorkspacePath: workspace, constants.GitRepositoryURL: "https://github.com/myorg/myrepo/"},
			expectedRelative:   "services/foo/foo_test.go",
			expectedFilesystem: "/ci/workspace/services/foo/foo_test.go",
			expectedKnown:      true,
		},
		{
			name:               "repository URL without git suffix",
			runtimePath:        "github.com/myorg/myrepo/services/foo/foo_test.go",
			tags:               map[string]string{constants.CIWorkspacePath: workspace, constants.GitRepositoryURL: "https://github.com/myorg/myrepo"},
			expectedRelative:   "services/foo/foo_test.go",
			expectedFilesystem: "/ci/workspace/services/foo/foo_test.go",
			expectedKnown:      true,
		},
		{
			name:               "azure devops textual repository URL",
			runtimePath:        "dev.azure.com/org/project/_git/repo/services/foo/foo_test.go",
			tags:               map[string]string{constants.CIWorkspacePath: workspace, constants.GitRepositoryURL: "https://dev.azure.com/org/project/_git/repo"},
			expectedRelative:   "services/foo/foo_test.go",
			expectedFilesystem: "/ci/workspace/services/foo/foo_test.go",
			expectedKnown:      true,
		},
		{
			name:               "segment boundary safety",
			runtimePath:        "github.com/myorg/myrepository/foo_test.go",
			tags:               map[string]string{constants.CIWorkspacePath: workspace, constants.GitRepositoryURL: "https://github.com/myorg/myrepo.git"},
			expectedRelative:   "github.com/myorg/myrepository/foo_test.go",
			expectedFilesystem: "github.com/myorg/myrepository/foo_test.go",
			expectedKnown:      false,
		},
		{
			name:               "semantic import version from main module path",
			runtimePath:        "github.com/DataDog/dd-trace-go/v2/internal/civisibility/foo_test.go",
			tags:               map[string]string{constants.CIWorkspacePath: workspace, constants.GitRepositoryURL: "https://github.com/DataDog/dd-trace-go.git"},
			mainModulePath:     "github.com/DataDog/dd-trace-go/v2",
			expectedRelative:   "internal/civisibility/foo_test.go",
			expectedFilesystem: "/ci/workspace/internal/civisibility/foo_test.go",
			expectedKnown:      true,
			unexpectedRelative: []string{"v2/internal/civisibility/foo_test.go"},
		},
		{
			name:               "two digit semantic import version from main module path",
			runtimePath:        "github.com/org/repo/v10/pkg/foo_test.go",
			tags:               map[string]string{constants.CIWorkspacePath: workspace, constants.GitRepositoryURL: "https://github.com/org/repo.git"},
			mainModulePath:     "github.com/org/repo/v10",
			expectedRelative:   "pkg/foo_test.go",
			expectedFilesystem: "/ci/workspace/pkg/foo_test.go",
			expectedKnown:      true,
			unexpectedRelative: []string{"v10/pkg/foo_test.go"},
		},
		{
			name:               "ambiguous physical v2 subdirectory uses documented root module default",
			runtimePath:        "github.com/org/repo/v2/pkg/foo_test.go",
			tags:               map[string]string{constants.CIWorkspacePath: workspace, constants.GitRepositoryURL: "https://github.com/org/repo.git"},
			mainModulePath:     "github.com/org/repo/v2",
			expectedRelative:   "pkg/foo_test.go",
			expectedFilesystem: "/ci/workspace/pkg/foo_test.go",
			expectedKnown:      true,
		},
		{
			name:                "semantic import version in monorepo module",
			runtimePath:         "github.com/org/repo/services/foo/v2/pkg/foo_test.go",
			tags:                map[string]string{constants.CIWorkspacePath: workspace, constants.GitRepositoryURL: "https://github.com/org/repo.git"},
			mainModulePath:      "github.com/org/repo/services/foo/v2",
			expectedRelative:    "services/foo/pkg/foo_test.go",
			expectedFilesystem:  "/ci/workspace/services/foo/pkg/foo_test.go",
			expectedKnown:       true,
			unexpectedRelative:  []string{"pkg/foo_test.go", "services/foo/v2/pkg/foo_test.go"},
			unexpectedFilePaths: []string{"/ci/workspace/pkg/foo_test.go", "/ci/workspace/services/foo/v2/pkg/foo_test.go"},
		},
		{
			name:                "two digit semantic import version in monorepo module",
			runtimePath:         "github.com/org/repo/services/foo/v10/pkg/foo_test.go",
			tags:                map[string]string{constants.CIWorkspacePath: workspace, constants.GitRepositoryURL: "https://github.com/org/repo.git"},
			mainModulePath:      "github.com/org/repo/services/foo/v10",
			expectedRelative:    "services/foo/pkg/foo_test.go",
			expectedFilesystem:  "/ci/workspace/services/foo/pkg/foo_test.go",
			expectedKnown:       true,
			unexpectedRelative:  []string{"pkg/foo_test.go", "services/foo/v10/pkg/foo_test.go"},
			unexpectedFilePaths: []string{"/ci/workspace/pkg/foo_test.go", "/ci/workspace/services/foo/v10/pkg/foo_test.go"},
		},
		{
			name:               "semantic import version not inferred without build info",
			runtimePath:        "github.com/DataDog/dd-trace-go/v2/internal/civisibility/foo_test.go",
			tags:               map[string]string{constants.CIWorkspacePath: workspace, constants.GitRepositoryURL: "https://github.com/DataDog/dd-trace-go.git"},
			expectedRelative:   "v2/internal/civisibility/foo_test.go",
			expectedFilesystem: "/ci/workspace/v2/internal/civisibility/foo_test.go",
			expectedKnown:      true,
		},
		{
			name:               "v1 is not a semantic import suffix",
			runtimePath:        "github.com/myorg/myrepo/v1/pkg/foo_test.go",
			tags:               map[string]string{constants.CIWorkspacePath: workspace, constants.GitRepositoryURL: "https://github.com/myorg/myrepo.git"},
			mainModulePath:     "github.com/myorg/myrepo/v1",
			expectedRelative:   "v1/pkg/foo_test.go",
			expectedFilesystem: "/ci/workspace/v1/pkg/foo_test.go",
			expectedKnown:      true,
		},
		{
			name:               "leading zero version is not a semantic import suffix",
			runtimePath:        "github.com/myorg/myrepo/v02/pkg/foo_test.go",
			tags:               map[string]string{constants.CIWorkspacePath: workspace, constants.GitRepositoryURL: "https://github.com/myorg/myrepo.git"},
			mainModulePath:     "github.com/myorg/myrepo/v02",
			expectedRelative:   "v02/pkg/foo_test.go",
			expectedFilesystem: "/ci/workspace/v02/pkg/foo_test.go",
			expectedKnown:      true,
		},
		{
			name:               "v2x is not a semantic import suffix",
			runtimePath:        "github.com/myorg/myrepo/v2x/pkg/foo_test.go",
			tags:               map[string]string{constants.CIWorkspacePath: workspace, constants.GitRepositoryURL: "https://github.com/myorg/myrepo.git"},
			mainModulePath:     "github.com/myorg/myrepo/v2x",
			expectedRelative:   "v2x/pkg/foo_test.go",
			expectedFilesystem: "/ci/workspace/v2x/pkg/foo_test.go",
			expectedKnown:      true,
		},
		{
			name:               "nested v2 directory is not stripped without matching module path",
			runtimePath:        "github.com/myorg/myrepo/pkg/v2/foo_test.go",
			tags:               map[string]string{constants.CIWorkspacePath: workspace, constants.GitRepositoryURL: "https://github.com/myorg/myrepo.git"},
			mainModulePath:     "github.com/myorg/myrepo",
			expectedRelative:   "pkg/v2/foo_test.go",
			expectedFilesystem: "/ci/workspace/pkg/v2/foo_test.go",
			expectedKnown:      true,
		},
		{
			name:               "build info fallback stays module relative",
			runtimePath:        "github.com/myorg/myrepo/pkg/foo_test.go",
			tags:               map[string]string{constants.CIWorkspacePath: workspace},
			mainModulePath:     "github.com/myorg/myrepo",
			expectedRelative:   "pkg/foo_test.go",
			expectedFilesystem: "github.com/myorg/myrepo/pkg/foo_test.go",
			expectedKnown:      false,
		},
		{
			name:               "repository URL preferred over build info",
			runtimePath:        "github.com/myorg/myrepo/services/foo/foo_test.go",
			tags:               map[string]string{constants.CIWorkspacePath: workspace, constants.GitRepositoryURL: "https://github.com/myorg/myrepo.git"},
			mainModulePath:     "github.com/myorg/myrepo/services/foo",
			expectedRelative:   "services/foo/foo_test.go",
			expectedFilesystem: "/ci/workspace/services/foo/foo_test.go",
			expectedKnown:      true,
			unexpectedRelative: []string{"foo_test.go"},
		},
		{
			name:               "unknown import like logical path",
			runtimePath:        "github.com/other/repo/pkg/foo_test.go",
			expectedRelative:   "github.com/other/repo/pkg/foo_test.go",
			expectedFilesystem: "github.com/other/repo/pkg/foo_test.go",
			expectedKnown:      false,
		},
		{
			name:               "unmatched import like logical path with workspace",
			runtimePath:        "github.com/other/repo/pkg/foo_test.go",
			tags:               map[string]string{constants.CIWorkspacePath: workspace},
			expectedRelative:   "github.com/other/repo/pkg/foo_test.go",
			expectedFilesystem: "github.com/other/repo/pkg/foo_test.go",
			expectedKnown:      false,
		},
		{
			name:               "backslash logical path",
			runtimePath:        `github.com\myorg\myrepo\pkg\foo_test.go`,
			tags:               map[string]string{constants.CIWorkspacePath: workspace, constants.GitRepositoryURL: "https://github.com/myorg/myrepo.git"},
			expectedRelative:   "pkg/foo_test.go",
			expectedFilesystem: "/ci/workspace/pkg/foo_test.go",
			expectedKnown:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resolveSourceFilePath(tt.runtimePath, tt.tags, tt.mainModulePath)

			assert.Equal(t, tt.runtimePath, result.RuntimePath)
			assert.Equal(t, tt.expectedRelative, result.RelativePath)
			if tt.expectedFilesystem == "" {
				assert.Equal(t, tt.expectedFilesystem, result.FilesystemPath)
			} else {
				assert.Equal(t, filepath.Clean(tt.expectedFilesystem), filepath.Clean(result.FilesystemPath))
			}
			assert.Equal(t, tt.expectedKnown, result.FilesystemKnown)
			for _, unexpected := range tt.unexpectedRelative {
				assert.NotEqual(t, unexpected, result.RelativePath)
			}
			for _, unexpected := range tt.unexpectedFilePaths {
				assert.NotEqual(t, filepath.Clean(unexpected), filepath.Clean(result.FilesystemPath))
			}
		})
	}
}

func TestRepositoryPathFromURL(t *testing.T) {
	tests := map[string]string{
		"https://github.com/myorg/myrepo.git":           "github.com/myorg/myrepo",
		"https://github.com/myorg/myrepo":               "github.com/myorg/myrepo",
		"https://github.com/myorg/myrepo/":              "github.com/myorg/myrepo",
		"https://token@github.com/myorg/myrepo.git":     "github.com/myorg/myrepo",
		"https://user:pass@github.com/myorg/myrepo.git": "github.com/myorg/myrepo",
		"ssh://git@github.com/myorg/myrepo.git":         "github.com/myorg/myrepo",
		"ssh://git@github.com:2222/myorg/myrepo.git":    "github.com/myorg/myrepo",
		"git@github.com:myorg/myrepo.git":               "github.com/myorg/myrepo",
		"git@github.com:myorg/myrepo":                   "github.com/myorg/myrepo",
		"https://dev.azure.com/org/project/_git/repo":   "dev.azure.com/org/project/_git/repo",
	}

	for repositoryURL, expectedPath := range tests {
		t.Run(repositoryURL, func(t *testing.T) {
			assert.Equal(t, expectedPath, repositoryPathFromURL(repositoryURL))
		})
	}
}

func TestResolveSourceFilePathFromCITags(t *testing.T) {
	ResetCITags()
	t.Cleanup(ResetCITags)
	originalCiTags = map[string]string{
		constants.CIWorkspacePath:  "/ci/workspace",
		constants.GitRepositoryURL: "https://github.com/myorg/myrepo.git",
	}

	result := ResolveSourceFilePathFromCITags("github.com/myorg/myrepo/pkg/foo_test.go")

	assert.Equal(t, "pkg/foo_test.go", result.RelativePath)
	assert.Equal(t, filepath.Clean("/ci/workspace/pkg/foo_test.go"), filepath.Clean(result.FilesystemPath))
	assert.True(t, result.FilesystemKnown)
}

func TestResolvedTrimpathSourceFileMatchesCodeOwners(t *testing.T) {
	ResetCITags()
	ResetCodeOwnersForTesting()
	t.Cleanup(func() {
		ResetCITags()
		ResetCodeOwnersForTesting()
	})

	workspace := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workspace, "CODEOWNERS"), []byte("/services/foo/* @team/foo\n"), 0o600))
	originalCiTags = map[string]string{
		constants.CIWorkspacePath:  workspace,
		constants.GitRepositoryURL: "https://github.com/myorg/myrepo.git",
	}

	sourcePath := resolveSourceFilePath(
		"github.com/myorg/myrepo/v2/services/foo/foo_test.go",
		originalCiTags,
		"github.com/myorg/myrepo/v2",
	)
	codeOwners := GetCodeOwners()
	require.NotNil(t, codeOwners)

	match, found := codeOwners.Match("/" + sourcePath.RelativePath)
	require.True(t, found)
	assert.Equal(t, "services/foo/foo_test.go", sourcePath.RelativePath)
	assert.Equal(t, "[\"@team/foo\"]", match.GetOwnersString())
}

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package utils

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/bazel"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"

	"github.com/stretchr/testify/assert"
)

func TestGetCITagsCache(t *testing.T) {
	ResetCITags()
	originalCiTags = map[string]string{"key": "value"}

	// First call to initialize ciTags
	tags := GetCITags()
	assert.Equal(t, "value", tags["key"])

	tags["key"] = "newvalue"
	tags = GetCITags()
	assert.Equal(t, "newvalue", tags["key"])
}

func TestAddCITags(t *testing.T) {
	ResetCITags()
	originalCiTags = map[string]string{"key": "value"}

	// First call to initialize ciTags
	tags := GetCITags()
	assert.Equal(t, "value", tags["key"])

	AddCITags("key", "newvalue")
	AddCITags("key2", "value2")
	tags = GetCITags()
	assert.Equal(t, "newvalue", tags["key"])
	assert.Equal(t, "value2", tags["key2"])
}

func TestAddCITagsMap(t *testing.T) {
	ResetCITags()
	originalCiTags = map[string]string{"key": "value"}

	// First call to initialize ciTags
	tags := GetCITags()
	assert.Equal(t, "value", tags["key"])

	nmap := map[string]string{}
	nmap["key"] = "newvalue"
	nmap["key2"] = "value2"
	AddCITagsMap(nmap)
	tags = GetCITags()
	assert.Equal(t, "newvalue", tags["key"])
	assert.Equal(t, "value2", tags["key2"])
}

func TestGetCIMetricsCache(t *testing.T) {
	ResetCIMetrics()
	originalCiMetrics = map[string]float64{"key": float64(1)}

	// First call to initialize ciMetrics
	tags := GetCIMetrics()
	assert.Equal(t, float64(1), tags["key"])

	tags["key"] = float64(42)
	tags = GetCIMetrics()
	assert.Equal(t, float64(42), tags["key"])
}

func TestAddCIMetrics(t *testing.T) {
	ResetCIMetrics()
	originalCiMetrics = map[string]float64{"key": float64(1)}

	// First call to initialize ciMetrics
	tags := GetCIMetrics()
	assert.Equal(t, float64(1), tags["key"])

	AddCIMetrics("key", float64(42))
	AddCIMetrics("key2", float64(2))
	tags = GetCIMetrics()
	assert.Equal(t, float64(42), tags["key"])
	assert.Equal(t, float64(2), tags["key2"])
}

func TestAddCIMetricsMap(t *testing.T) {
	ResetCIMetrics()
	originalCiMetrics = map[string]float64{"key": float64(1)}

	// First call to initialize ciMetrics
	tags := GetCIMetrics()
	assert.Equal(t, float64(1), tags["key"])

	nmap := map[string]float64{}
	nmap["key"] = float64(42)
	nmap["key2"] = float64(2)
	AddCIMetricsMap(nmap)
	tags = GetCIMetrics()
	assert.Equal(t, float64(42), tags["key"])
	assert.Equal(t, float64(2), tags["key2"])
}

func TestGetRelativePathFromCITagsSourceRoot(t *testing.T) {
	ResetCITags()
	t.Cleanup(ResetCITags)
	originalCiTags = map[string]string{constants.CIWorkspacePath: "/ci/workspace"}

	absPath := "/ci/workspace/subdir/file.txt"
	expectedRelPath := "subdir/file.txt"

	relPath := GetRelativePathFromCITagsSourceRoot(absPath)
	assert.Equal(t, expectedRelPath, relPath)

	outsidePath := "/tmp/build/file.txt"
	expectedOutsideRelPath, err := filepath.Rel("/ci/workspace", outsidePath)
	assert.NoError(t, err)
	relPath = GetRelativePathFromCITagsSourceRoot(outsidePath)
	assert.Equal(t, filepath.ToSlash(expectedOutsideRelPath), relPath)

	modulePath := "github.com/myorg/myrepo/pkg/file_test.go"
	relPath = GetRelativePathFromCITagsSourceRoot(modulePath)
	assert.Equal(t, modulePath, relPath)

	// Test case when CIWorkspacePath is not set in ciTags
	originalCiTags = map[string]string{}
	currentCiTags = nil
	relPath = GetRelativePathFromCITagsSourceRoot(absPath)
	assert.Equal(t, absPath, relPath)
}

func TestGetCITagsUsesGitEnrichmentOutsidePayloadFilesMode(t *testing.T) {
	ResetCITags()
	bazel.ResetForTesting()
	t.Cleanup(ResetCITags)
	t.Cleanup(bazel.ResetForTesting)

	originalGetProviderTagsFunc := getProviderTagsFunc
	originalGetLocalGitDataFunc := getLocalGitDataFunc
	originalFetchCommitDataFunc := fetchCommitDataFunc
	originalApplyEnvironmentalDataIfRequiredFunc := applyEnvironmentalDataIfRequiredFunc
	t.Cleanup(func() {
		getProviderTagsFunc = originalGetProviderTagsFunc
		getLocalGitDataFunc = originalGetLocalGitDataFunc
		fetchCommitDataFunc = originalFetchCommitDataFunc
		applyEnvironmentalDataIfRequiredFunc = originalApplyEnvironmentalDataIfRequiredFunc
	})

	var getLocalGitDataCalls int
	var fetchCommitDataCalls int
	var applyEnvironmentalDataCalls int

	getProviderTagsFunc = func() map[string]string {
		return map[string]string{
			constants.CIJobName:     "job-name",
			constants.GitHeadCommit: "head-sha",
		}
	}
	getLocalGitDataFunc = func() (localGitData, error) {
		getLocalGitDataCalls++
		return localGitData{
			localCommitData: localCommitData{
				CommitSha:     "commit-sha",
				CommitMessage: "commit-message",
			},
			SourceRoot:    "/tmp/workspace",
			RepositoryURL: "https://example.com/repo.git",
			Branch:        "main",
		}, nil
	}
	fetchCommitDataFunc = func(commitSha string) (localCommitData, error) {
		fetchCommitDataCalls++
		assert.Equal(t, "head-sha", commitSha)
		return localCommitData{
			CommitSha:     "head-sha",
			CommitMessage: "head-message",
		}, nil
	}
	applyEnvironmentalDataIfRequiredFunc = func(tags map[string]string) {
		applyEnvironmentalDataCalls++
		tags["env.applied"] = "true"
	}

	tags := GetCITags()
	assert.Equal(t, 1, getLocalGitDataCalls)
	assert.Equal(t, 1, fetchCommitDataCalls)
	assert.Equal(t, 1, applyEnvironmentalDataCalls)
	assert.Equal(t, "/tmp/workspace", tags[constants.CIWorkspacePath])
	assert.Equal(t, "https://example.com/repo.git", tags[constants.GitRepositoryURL])
	assert.Equal(t, "commit-sha", tags[constants.GitCommitSHA])
	assert.Equal(t, "head-message", tags[constants.GitHeadMessage])
	assert.Equal(t, "true", tags["env.applied"])
}

func TestGetCITagsWorkspacePrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, provider, local, file, expected string
	}{
		{"provider", "/provider", "/checkout", "/old-checkout", "/provider"},
		{"local", "", "/checkout", "/old-checkout", "/checkout"},
		{"file", "", "", "/old-checkout", "/old-checkout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ResetCITags()
			t.Cleanup(ResetCITags)
			bazel.ResetForTesting()
			t.Cleanup(bazel.ResetForTesting)
			originalProvider, originalGit, originalEnvironmental := getProviderTagsFunc, getLocalGitDataFunc, applyEnvironmentalDataIfRequiredFunc
			t.Cleanup(func() {
				getProviderTagsFunc, getLocalGitDataFunc, applyEnvironmentalDataIfRequiredFunc = originalProvider, originalGit, originalEnvironmental
			})
			getProviderTagsFunc = func() map[string]string {
				tags := map[string]string{}
				if tc.provider != "" {
					tags[constants.CIWorkspacePath] = tc.provider
				}
				return tags
			}
			getLocalGitDataFunc = func() (localGitData, error) {
				return localGitData{SourceRoot: tc.local}, nil
			}
			applyEnvironmentalDataIfRequiredFunc = func(tags map[string]string) {
				if _, ok := tags[constants.CIWorkspacePath]; !ok {
					tags[constants.CIWorkspacePath] = tc.file
				}
			}
			assert.Equal(t, tc.expected, GetCITags()[constants.CIWorkspacePath])
		})
	}
}

func TestGetCITagsSkipsGitEnrichmentInPayloadFilesMode(t *testing.T) {
	ResetCITags()
	bazel.ResetForTesting()
	t.Cleanup(ResetCITags)
	t.Cleanup(bazel.ResetForTesting)

	t.Setenv(bazel.PayloadsInFilesEnv, "true")
	t.Setenv(bazel.UndeclaredOutputsDirEnv, t.TempDir())
	bazel.ResetForTesting()

	originalGetProviderTagsFunc := getProviderTagsFunc
	originalGetLocalGitDataFunc := getLocalGitDataFunc
	originalFetchCommitDataFunc := fetchCommitDataFunc
	originalApplyEnvironmentalDataIfRequiredFunc := applyEnvironmentalDataIfRequiredFunc
	t.Cleanup(func() {
		getProviderTagsFunc = originalGetProviderTagsFunc
		getLocalGitDataFunc = originalGetLocalGitDataFunc
		fetchCommitDataFunc = originalFetchCommitDataFunc
		applyEnvironmentalDataIfRequiredFunc = originalApplyEnvironmentalDataIfRequiredFunc
	})

	var getLocalGitDataCalls int
	var fetchCommitDataCalls int
	var applyEnvironmentalDataCalls int

	getProviderTagsFunc = func() map[string]string {
		return map[string]string{
			constants.CIJobName:     "job-name",
			constants.GitHeadCommit: "head-sha",
		}
	}
	getLocalGitDataFunc = func() (localGitData, error) {
		getLocalGitDataCalls++
		return localGitData{
			localCommitData: localCommitData{
				CommitSha:     "commit-sha",
				CommitMessage: "commit-message",
			},
			SourceRoot:    "/tmp/workspace",
			RepositoryURL: "https://example.com/repo.git",
			Branch:        "main",
		}, nil
	}
	fetchCommitDataFunc = func(commitSha string) (localCommitData, error) {
		fetchCommitDataCalls++
		assert.Equal(t, "head-sha", commitSha)
		return localCommitData{
			CommitSha:     "head-sha",
			CommitMessage: "head-message",
		}, nil
	}
	applyEnvironmentalDataIfRequiredFunc = func(tags map[string]string) {
		applyEnvironmentalDataCalls++
		tags[constants.CIWorkspacePath] = "/tmp/workspace-from-env"
		tags[constants.GitRepositoryURL] = "https://example.com/repo-from-env.git"
		tags[constants.GitCommitSHA] = "commit-sha-from-env"
		tags[constants.GitBranch] = "main-from-env"
		tags[constants.GitCommitMessage] = "commit-message-from-env"
		tags["env.applied"] = "true"
	}

	tags := GetCITags()
	assert.Equal(t, 0, getLocalGitDataCalls)
	assert.Equal(t, 0, fetchCommitDataCalls)
	assert.Equal(t, 1, applyEnvironmentalDataCalls)
	assert.Contains(t, tags, constants.TestCommand)
	assert.Contains(t, tags, constants.TestSessionName)
	assert.Equal(t, "/tmp/workspace-from-env", tags[constants.CIWorkspacePath])
	assert.Equal(t, "https://example.com/repo-from-env.git", tags[constants.GitRepositoryURL])
	assert.Equal(t, "commit-sha-from-env", tags[constants.GitCommitSHA])
	assert.Equal(t, "main-from-env", tags[constants.GitBranch])
	assert.Equal(t, "commit-message-from-env", tags[constants.GitCommitMessage])
	assert.NotContains(t, tags, constants.GitHeadMessage)
	assert.Equal(t, "true", tags["env.applied"])
	assert.Equal(t, "job-name-"+tags[constants.TestCommand], tags[constants.TestSessionName])
}

func TestGetCITagsAddsBazelProviderInPayloadFilesModeWithoutProvider(t *testing.T) {
	ResetCITags()
	bazel.ResetForTesting()
	t.Cleanup(ResetCITags)
	t.Cleanup(bazel.ResetForTesting)

	t.Setenv(bazel.PayloadsInFilesEnv, "true")
	t.Setenv(bazel.UndeclaredOutputsDirEnv, t.TempDir())
	bazel.ResetForTesting()

	originalGetProviderTagsFunc := getProviderTagsFunc
	originalApplyEnvironmentalDataIfRequiredFunc := applyEnvironmentalDataIfRequiredFunc
	t.Cleanup(func() {
		getProviderTagsFunc = originalGetProviderTagsFunc
		applyEnvironmentalDataIfRequiredFunc = originalApplyEnvironmentalDataIfRequiredFunc
	})

	getProviderTagsFunc = func() map[string]string {
		return map[string]string{}
	}
	applyEnvironmentalDataIfRequiredFunc = func(tags map[string]string) {}

	tags := GetCITags()
	assert.Equal(t, "bazel", tags[constants.CIProviderName])
}

func TestGetCITagsPreservesDetectedProviderInPayloadFilesMode(t *testing.T) {
	ResetCITags()
	bazel.ResetForTesting()
	t.Cleanup(ResetCITags)
	t.Cleanup(bazel.ResetForTesting)

	t.Setenv(bazel.PayloadsInFilesEnv, "true")
	t.Setenv(bazel.UndeclaredOutputsDirEnv, t.TempDir())
	bazel.ResetForTesting()

	originalGetProviderTagsFunc := getProviderTagsFunc
	originalApplyEnvironmentalDataIfRequiredFunc := applyEnvironmentalDataIfRequiredFunc
	t.Cleanup(func() {
		getProviderTagsFunc = originalGetProviderTagsFunc
		applyEnvironmentalDataIfRequiredFunc = originalApplyEnvironmentalDataIfRequiredFunc
	})

	getProviderTagsFunc = func() map[string]string {
		return map[string]string{constants.CIProviderName: "github"}
	}
	applyEnvironmentalDataIfRequiredFunc = func(tags map[string]string) {}

	tags := GetCITags()
	assert.Equal(t, "github", tags[constants.CIProviderName])
}

func TestGetCITagsPreservesEnvironmentalDataProviderInPayloadFilesMode(t *testing.T) {
	ResetCITags()
	bazel.ResetForTesting()
	t.Cleanup(ResetCITags)
	t.Cleanup(bazel.ResetForTesting)

	t.Setenv(bazel.PayloadsInFilesEnv, "true")
	t.Setenv(bazel.UndeclaredOutputsDirEnv, t.TempDir())
	bazel.ResetForTesting()

	originalGetProviderTagsFunc := getProviderTagsFunc
	originalApplyEnvironmentalDataIfRequiredFunc := applyEnvironmentalDataIfRequiredFunc
	t.Cleanup(func() {
		getProviderTagsFunc = originalGetProviderTagsFunc
		applyEnvironmentalDataIfRequiredFunc = originalApplyEnvironmentalDataIfRequiredFunc
	})

	getProviderTagsFunc = func() map[string]string {
		return map[string]string{}
	}
	applyEnvironmentalDataIfRequiredFunc = func(tags map[string]string) {
		tags[constants.CIProviderName] = "github"
	}

	tags := GetCITags()
	assert.Equal(t, "github", tags[constants.CIProviderName])
}

func TestGetCITagsAddsBazelProviderInManifestModeWithoutProvider(t *testing.T) {
	ResetCITags()
	bazel.ResetForTesting()
	t.Cleanup(ResetCITags)
	t.Cleanup(bazel.ResetForTesting)

	manifestPath := filepath.Join(t.TempDir(), "manifest.txt")
	assert.NoError(t, os.WriteFile(manifestPath, []byte("version=1\n"), 0o644))
	t.Setenv(bazel.ManifestFilePathEnv, manifestPath)
	bazel.ResetForTesting()

	originalGetProviderTagsFunc := getProviderTagsFunc
	originalGetLocalGitDataFunc := getLocalGitDataFunc
	originalFetchCommitDataFunc := fetchCommitDataFunc
	originalApplyEnvironmentalDataIfRequiredFunc := applyEnvironmentalDataIfRequiredFunc
	t.Cleanup(func() {
		getProviderTagsFunc = originalGetProviderTagsFunc
		getLocalGitDataFunc = originalGetLocalGitDataFunc
		fetchCommitDataFunc = originalFetchCommitDataFunc
		applyEnvironmentalDataIfRequiredFunc = originalApplyEnvironmentalDataIfRequiredFunc
	})

	getProviderTagsFunc = func() map[string]string {
		return map[string]string{}
	}
	getLocalGitDataFunc = func() (localGitData, error) {
		return localGitData{}, nil
	}
	fetchCommitDataFunc = func(commitSha string) (localCommitData, error) {
		return localCommitData{}, nil
	}
	applyEnvironmentalDataIfRequiredFunc = func(tags map[string]string) {}

	tags := GetCITags()
	assert.Equal(t, "bazel", tags[constants.CIProviderName])
}

func TestGetCITagsDoesNotAddBazelProviderOutsideBazelMode(t *testing.T) {
	ResetCITags()
	bazel.ResetForTesting()
	t.Cleanup(ResetCITags)
	t.Cleanup(bazel.ResetForTesting)

	originalGetProviderTagsFunc := getProviderTagsFunc
	originalGetLocalGitDataFunc := getLocalGitDataFunc
	originalFetchCommitDataFunc := fetchCommitDataFunc
	originalApplyEnvironmentalDataIfRequiredFunc := applyEnvironmentalDataIfRequiredFunc
	t.Cleanup(func() {
		getProviderTagsFunc = originalGetProviderTagsFunc
		getLocalGitDataFunc = originalGetLocalGitDataFunc
		fetchCommitDataFunc = originalFetchCommitDataFunc
		applyEnvironmentalDataIfRequiredFunc = originalApplyEnvironmentalDataIfRequiredFunc
	})

	getProviderTagsFunc = func() map[string]string {
		return map[string]string{}
	}
	getLocalGitDataFunc = func() (localGitData, error) {
		return localGitData{}, nil
	}
	fetchCommitDataFunc = func(commitSha string) (localCommitData, error) {
		return localCommitData{}, nil
	}
	applyEnvironmentalDataIfRequiredFunc = func(tags map[string]string) {}

	tags := GetCITags()
	assert.NotContains(t, tags, constants.CIProviderName)
}

func TestShaOverrideDoesNotRestoreInferredProviderTag(t *testing.T) {
	for _, tc := range []struct{ name, tag, branch, wantTag string }{
		{name: "inferred"},
		{name: "explicit-tag", tag: "explicit-tag", wantTag: "explicit-tag"},
		{name: "explicit-branch", branch: "refs/tags/explicit-tag", wantTag: "explicit-tag"},
		{name: "explicit-tag-and-branch", tag: "explicit-tag", branch: "refs/tags/branch-tag", wantTag: "explicit-tag"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ResetCITags()
			t.Cleanup(ResetCITags)
			bazel.ResetForTesting()
			t.Cleanup(bazel.ResetForTesting)
			t.Setenv("DD_GIT_COMMIT_SHA", strings.Repeat("c", 40))
			t.Setenv("DD_GIT_TAG", tc.tag)
			t.Setenv("DD_GIT_BRANCH", tc.branch)
			originalProvider, originalGit, originalFetch, originalEnvironmental := getProviderTagsFunc, getLocalGitDataFunc, fetchCommitDataFunc, applyEnvironmentalDataIfRequiredFunc
			t.Cleanup(func() {
				getProviderTagsFunc, getLocalGitDataFunc, fetchCommitDataFunc, applyEnvironmentalDataIfRequiredFunc = originalProvider, originalGit, originalFetch, originalEnvironmental
			})
			getProviderTagsFunc = func() map[string]string {
				return map[string]string{constants.GitCommitSHA: strings.Repeat("b", 40), constants.GitBranch: "refs/tags/provider-tag", constants.GitTag: "provider-tag", constants.CIJobName: "tests", constants.GitHeadCommit: "pr-head"}
			}
			getLocalGitDataFunc = func() (localGitData, error) { return localGitData{}, errors.New("checkout unavailable") }
			fetchCommitDataFunc = func(sha string) (localCommitData, error) { return localCommitData{CommitSha: sha}, nil }
			applyEnvironmentalDataIfRequiredFunc = func(map[string]string) {}
			tags := GetCITags()
			assert.Equal(t, strings.Repeat("c", 40), tags[constants.GitCommitSHA])
			if tc.wantTag == "" {
				assert.NotContains(t, tags, constants.GitTag)
			} else {
				assert.Equal(t, tc.wantTag, tags[constants.GitTag])
			}
			if tc.branch == "" {
				assert.NotContains(t, tags, constants.GitBranch)
			}
			assert.Equal(t, "tests", tags[constants.CIJobName])
			assert.Equal(t, "pr-head", tags[constants.GitHeadCommit])
		})
	}
}

func TestProviderShaIsPreservedUntilOverrideCleanup(t *testing.T) {
	for _, source := range []string{"changed", "same", "explicit", "payload-changed", "payload-same", "payload-explicit"} {
		t.Run(source, func(t *testing.T) {
			ResetCITags()
			t.Cleanup(ResetCITags)
			bazel.ResetForTesting()
			t.Cleanup(bazel.ResetForTesting)
			t.Setenv(bazel.PayloadsInFilesEnv, "false")
			if strings.HasPrefix(source, "payload-") {
				t.Setenv(bazel.PayloadsInFilesEnv, "true")
				t.Setenv(bazel.UndeclaredOutputsDirEnv, t.TempDir())
				source = strings.TrimPrefix(source, "payload-")
			}
			for key := range providers {
				t.Setenv(key, "")
				assert.NoError(t, os.Unsetenv(key))
			}
			for _, key := range []string{"DD_GIT_BRANCH", "DD_GIT_TAG", "DD_GIT_REPOSITORY_URL", "DD_GIT_COMMIT_MESSAGE", "DD_GIT_COMMIT_AUTHOR_NAME", "DD_GIT_COMMIT_AUTHOR_EMAIL", "DD_GIT_COMMIT_AUTHOR_DATE", "DD_GIT_COMMIT_COMMITTER_NAME", "DD_GIT_COMMIT_COMMITTER_EMAIL", "DD_GIT_COMMIT_COMMITTER_DATE", "DD_GIT_PULL_REQUEST_BASE_BRANCH", "DD_GIT_PULL_REQUEST_BASE_BRANCH_SHA"} {
				t.Setenv(key, "")
			}
			t.Setenv("GITLAB_CI", "true")
			t.Setenv("CI_COMMIT_SHA", strings.Repeat("b", 40))
			t.Setenv("CI_COMMIT_BRANCH", "refs/tags/provider-tag")
			t.Setenv("CI_COMMIT_TAG", "provider-tag")
			t.Setenv("CI_COMMIT_MESSAGE", "provider-message")
			t.Setenv("CI_COMMIT_AUTHOR", "Provider <provider@example.com>")
			t.Setenv("CI_COMMIT_TIMESTAMP", "2025-01-01T00:00:00Z")
			t.Setenv("CI_JOB_NAME", "tests")
			t.Setenv("CI_MERGE_REQUEST_SOURCE_BRANCH_SHA", strings.Repeat("d", 40))
			t.Setenv("CI_MERGE_REQUEST_TARGET_BRANCH_NAME", "main")
			selected := strings.Repeat("c", 40)
			if source == "same" {
				selected = strings.Repeat("b", 40)
			}
			t.Setenv("DD_GIT_COMMIT_SHA", selected)
			if source == "explicit" {
				t.Setenv("DD_GIT_COMMIT_MESSAGE", "explicit-message")
				t.Setenv("DD_GIT_COMMIT_AUTHOR_NAME", "Explicit")
				t.Setenv("DD_GIT_TAG", "explicit-tag")
			}
			originalProvider, originalGit, originalFetch, originalEnvironmental := getProviderTagsFunc, getLocalGitDataFunc, fetchCommitDataFunc, applyEnvironmentalDataIfRequiredFunc
			t.Cleanup(func() {
				getProviderTagsFunc, getLocalGitDataFunc, fetchCommitDataFunc, applyEnvironmentalDataIfRequiredFunc = originalProvider, originalGit, originalFetch, originalEnvironmental
			})
			getProviderTagsFunc = getProviderTags
			getLocalGitDataFunc = func() (localGitData, error) { return localGitData{}, errors.New("checkout unavailable") }
			fetchCommitDataFunc = func(string) (localCommitData, error) { return localCommitData{}, errors.New("checkout unavailable") }
			applyEnvironmentalDataIfRequiredFunc = func(map[string]string) {}

			tags := GetCITags()
			assert.Equal(t, selected, tags[constants.GitCommitSHA])
			assert.Equal(t, "tests", tags[constants.CIJobName])
			assert.Equal(t, strings.Repeat("d", 40), tags[constants.GitHeadCommit])
			assert.Equal(t, "main", tags[constants.GitPrBaseBranch])
			if source == "same" {
				assert.Equal(t, "provider-message", tags[constants.GitCommitMessage])
				assert.Equal(t, "Provider", tags[constants.GitCommitAuthorName])
				assert.Equal(t, "provider-tag", tags[constants.GitTag])
			} else {
				assert.NotContains(t, tags, constants.GitCommitAuthorEmail)
				assert.NotContains(t, tags, constants.GitCommitAuthorDate)
				assert.NotContains(t, tags, constants.GitBranch)
				if source == "explicit" {
					assert.Equal(t, "explicit-message", tags[constants.GitCommitMessage])
					assert.Equal(t, "Explicit", tags[constants.GitCommitAuthorName])
					assert.Equal(t, "explicit-tag", tags[constants.GitTag])
				} else {
					assert.NotContains(t, tags, constants.GitCommitMessage)
					assert.NotContains(t, tags, constants.GitCommitAuthorName)
					assert.NotContains(t, tags, constants.GitTag)
				}
			}
		})
	}
}

func TestGitMetadataPrecedence(t *testing.T) {
	for _, source := range []string{"git", "provider", "override", "partial", "sha-only", "uppercase-sha"} {
		t.Run(source, func(t *testing.T) {
			ResetCITags()
			t.Cleanup(ResetCITags)
			bazel.ResetForTesting()
			t.Cleanup(bazel.ResetForTesting)
			originalProvider, originalGit, originalFetch, originalEnvironmental := getProviderTagsFunc, getLocalGitDataFunc, fetchCommitDataFunc, applyEnvironmentalDataIfRequiredFunc
			t.Cleanup(func() {
				getProviderTagsFunc, getLocalGitDataFunc, fetchCommitDataFunc, applyEnvironmentalDataIfRequiredFunc = originalProvider, originalGit, originalFetch, originalEnvironmental
			})
			local := map[string]string{
				constants.GitCommitSHA: "local-sha", constants.GitRepositoryURL: "https://example.com/local.git",
				constants.GitBranch: "local-branch", constants.GitTag: "local-tag",
				constants.GitCommitMessage: "local-message", constants.GitCommitAuthorName: "local-author",
				constants.GitCommitAuthorEmail: "local-author@example.com", constants.GitCommitAuthorDate: time.Unix(1, 0).String(),
				constants.GitCommitCommitterName: "local-committer", constants.GitCommitCommitterEmail: "local-committer@example.com",
				constants.GitCommitCommitterDate: time.Unix(2, 0).String(),
			}
			provider := map[string]string{constants.CIJobName: "tests", constants.GitHeadCommit: "pr-head"}
			if source == "uppercase-sha" {
				local[constants.GitCommitSHA] = strings.Repeat("a", 40)
			}
			for key := range local {
				provider[key] = "provider-" + key
				value := ""
				if source == "override" {
					value = "user-" + key
				}
				t.Setenv("DD_"+strings.ToUpper(strings.ReplaceAll(key, ".", "_")), value)
			}
			if source == "sha-only" {
				t.Setenv("DD_GIT_COMMIT_SHA", "selected-sha")
			}
			if source == "uppercase-sha" {
				t.Setenv("DD_GIT_COMMIT_SHA", strings.ToUpper(local[constants.GitCommitSHA]))
			}
			getProviderTagsFunc = func() map[string]string { return maps.Clone(provider) }
			getLocalGitDataFunc = func() (localGitData, error) {
				if source == "provider" {
					return localGitData{}, errors.New("checkout unavailable")
				}
				data := localGitData{localCommitData: localCommitData{CommitSha: local[constants.GitCommitSHA]}}
				if source != "partial" {
					data.RepositoryURL, data.Branch, data.Tag = local[constants.GitRepositoryURL], "local-branch", "local-tag"
					data.CommitMessage = "local-message"
					data.AuthorName, data.AuthorEmail, data.AuthorDate = "local-author", "local-author@example.com", time.Unix(1, 0)
					data.CommitterName, data.CommitterEmail, data.CommitterDate = "local-committer", "local-committer@example.com", time.Unix(2, 0)
				}
				return data, nil
			}
			fetchCommitDataFunc = func(sha string) (localCommitData, error) { return localCommitData{CommitSha: sha}, nil }
			applyEnvironmentalDataIfRequiredFunc = func(tags map[string]string) {
				if tags[constants.GitCommitAuthorName] == "" {
					tags[constants.GitCommitAuthorName] = "environment-file-author"
				}
				tags[constants.CINodeName] = "environment-file-node"
			}
			tags := GetCITags()
			for key, value := range local {
				switch source {
				case "provider":
					assert.Equal(t, provider[key], tags[key], key)
				case "override":
					assert.Equal(t, "user-"+key, tags[key], key)
				case "sha-only":
					if key == constants.GitCommitSHA {
						assert.Equal(t, "selected-sha", tags[key], key)
					} else if key == constants.GitTag || strings.HasPrefix(key, "git.commit.") {
						assert.NotContains(t, tags, key)
					} else {
						assert.Equal(t, value, tags[key], key)
					}
				case "partial":
					assert.NotEqual(t, provider[key], tags[key], key)
				default:
					assert.Equal(t, value, tags[key], key)
				}
			}
			assert.Equal(t, "tests", tags[constants.CIJobName])
			assert.Equal(t, "environment-file-node", tags[constants.CINodeName])
			assert.Equal(t, "pr-head", tags[constants.GitHeadCommit])
		})
	}
}

func TestDetachedCheckoutTagRespectsCITrigger(t *testing.T) {
	for _, tc := range []struct {
		name, branch, providerTag, overrideTag, wantTag string
	}{
		{name: "branch job", branch: "refs/heads/main"},
		{name: "pull request job", branch: "refs/pull/42/merge"},
		{name: "tag job", providerTag: "v1", wantTag: "v1"},
		{name: "unknown trigger", wantTag: "v1"},
		{name: "explicit override", branch: "refs/heads/main", overrideTag: "user-tag", wantTag: "user-tag"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := usePackFilesGitFixture(t)
			runFixtureGit(t, repo, "tag", "v1")
			runFixtureGit(t, repo, "checkout", "--detach")
			sha := runFixtureGit(t, repo, "rev-parse", "HEAD")
			ResetCITags()
			t.Cleanup(ResetCITags)
			bazel.ResetForTesting()
			t.Cleanup(bazel.ResetForTesting)
			for _, name := range []string{"DD_GIT_COMMIT_SHA", "DD_GIT_BRANCH", "DD_GIT_TAG", "DD_GIT_REPOSITORY_URL", "DD_GIT_PULL_REQUEST_BASE_BRANCH", "DD_GIT_PULL_REQUEST_BASE_BRANCH_SHA"} {
				t.Setenv(name, "")
			}
			t.Setenv("DD_GIT_TAG", tc.overrideTag)
			originalProvider, originalEnvironmental := getProviderTagsFunc, applyEnvironmentalDataIfRequiredFunc
			t.Cleanup(func() {
				getProviderTagsFunc, applyEnvironmentalDataIfRequiredFunc = originalProvider, originalEnvironmental
			})
			getProviderTagsFunc = func() map[string]string {
				return map[string]string{
					constants.GitBranch: tc.branch, constants.GitTag: tc.providerTag,
					constants.CIJobName: "tests", constants.GitHeadCommit: sha,
					constants.GitPrBaseBranch: "main",
				}
			}
			applyEnvironmentalDataIfRequiredFunc = func(map[string]string) {}

			tags := GetCITags()
			assert.Equal(t, tc.wantTag, tags[constants.GitTag])
			assert.Empty(t, tags[constants.GitBranch])
			assert.Equal(t, sha, tags[constants.GitCommitSHA])
			assert.Equal(t, "tests", tags[constants.CIJobName])
			assert.Equal(t, sha, tags[constants.GitHeadCommit])
			assert.Equal(t, "main", tags[constants.GitPrBaseBranch])
		})
	}
}

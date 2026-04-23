// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package utils

import (
	"os"
	"path/filepath"
	"testing"

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
	originalCiTags = map[string]string{constants.CIWorkspacePath: "/ci/workspace"}

	absPath := "/ci/workspace/subdir/file.txt"
	expectedRelPath := "subdir/file.txt"

	relPath := GetRelativePathFromCITagsSourceRoot(absPath)
	assert.Equal(t, expectedRelPath, relPath)

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

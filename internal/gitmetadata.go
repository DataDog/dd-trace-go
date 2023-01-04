// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package internal

import (
	"os"
	"runtime/debug"
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

const (
	envEnabledFlag   = "DD_TRACE_GIT_METADATA_ENABLED"
	envRepositoryUrl = "DD_GIT_REPOSITORY_URL"
	envCommitSha     = "DD_GIT_COMMIT_SHA"
	envMainPackage   = "DD_MAIN_PACKAGE"
	envGlobalTags    = "DD_TAGS"

	tagRepositoryUrl = "git.repository_url"
	tagCommitSha     = "git.commit.sha"

	//traceTagRepositoryUrl = "_dd.git.repository_url"
	//traceTagCommitSha     = "_dd.git.commit.sha"
	traceTagRepositoryUrl = "git.repository_url"
	traceTagCommitSha     = "git.commit.sha"
)

var (
	lock = &sync.Mutex{}

	gitMetadataTags map[string]string
)

// Get git metadata from environment variables
func getTagsFromEnv() map[string]string {
	repository_url := os.Getenv(envRepositoryUrl)
	commit_sha := os.Getenv(envCommitSha)

	if repository_url == "" || commit_sha == "" {
		tags := ParseTagString(os.Getenv(envGlobalTags))

		repository_url = tags[tagRepositoryUrl]
		commit_sha = tags[tagCommitSha]
	}

	if repository_url == "" || commit_sha == "" {
		return nil
	}

	return map[string]string{
		tagRepositoryUrl: repository_url,
		tagCommitSha:     commit_sha,
	}
}

// getTagsFrom binalry extracts git metadata from binary metadata:
func getTagsFromBinary() map[string]string {
	res := make(map[string]string)
	info, ok := debug.ReadBuildInfo()
	if !ok {
		log.Warn("ReadBuildInfo failed, skip source code metadata extracting")
		return res
	}
	repository_url := info.Path
	vcs := ""
	commit_sha := ""
	for _, s := range info.Settings {
		if s.Key == "vcs" {
			vcs = s.Value
		} else if s.Key == "vcs.revision" {
			commit_sha = s.Value
		}
	}
	if vcs != "git" {
		log.Warn("Unknown VCS: '%s', skip source code metadata extracting", vcs)
		return res
	}
	res[tagCommitSha] = commit_sha
	res[tagRepositoryUrl] = repository_url
	return res
}

// GetGitMetadataTags returns git metadata tags
func GetGitMetadataTags() map[string]string {
	if gitMetadataTags != nil {
		return gitMetadataTags
	}
	lock.Lock()
	defer lock.Unlock()
	if gitMetadataTags != nil {
		return gitMetadataTags
	}
	if BoolEnv(envEnabledFlag, true) {
		tags := getTagsFromEnv()
		if tags == nil {
			tags = getTagsFromBinary()
		}
		gitMetadataTags = tags
	} else {
		gitMetadataTags = make(map[string]string)
	}
	return gitMetadataTags
}

// CleanTags cleans up tags from git metadata
func CleanGitMetadataTags(tags map[string]string) {
	delete(tags, tagRepositoryUrl)
	delete(tags, tagCommitSha)
}

// GetTracerGitMetadataTags returns git metadata tags for tracer
// NB: Currently tracer inject tags with some workaround
// (only with _dd prefix and only for the first span in payload)
// So we provide different tag names
func GetTracerGitMetadataTags() map[string]string {
	res := make(map[string]string)
	tags := GetGitMetadataTags()

	repository_url := tags[tagRepositoryUrl]
	commit_sha := tags[tagCommitSha]

	if repository_url == "" || commit_sha == "" {
		return res
	}

	res[traceTagCommitSha] = commit_sha
	res[traceTagRepositoryUrl] = repository_url
	return res
}

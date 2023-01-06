// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package internal

import (
	"os"
	"sync"
)

const (
	envEnabledFlag   = "DD_TRACE_GIT_METADATA_ENABLED"
	envRepositoryURL = "DD_GIT_REPOSITORY_URL"
	envCommitSha     = "DD_GIT_COMMIT_SHA"
	envMainPackage   = "DD_MAIN_PACKAGE"
	envGlobalTags    = "DD_TAGS"

	tagRepositoryURL = "git.repository_url"
	tagCommitSha     = "git.commit.sha"

	traceTagRepositoryURL = "_dd.git.repository_url"
	traceTagCommitSha     = "_dd.git.commit.sha"
)

var (
	lock = &sync.Mutex{}

	gitMetadataTags map[string]string
)

// Get git metadata from environment variables
func getTagsFromEnv() map[string]string {
	repositoryURL := os.Getenv(envRepositoryURL)
	commitSha := os.Getenv(envCommitSha)

	if repositoryURL == "" || commitSha == "" {
		tags := ParseTagString(os.Getenv(envGlobalTags))

		repositoryURL = tags[tagRepositoryURL]
		commitSha = tags[tagCommitSha]
	}

	if repositoryURL == "" || commitSha == "" {
		return nil
	}

	return map[string]string{
		tagRepositoryURL: repositoryURL,
		tagCommitSha:     commitSha,
	}
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

// CleanGitMetadataTags cleans up tags from git metadata
func CleanGitMetadataTags(tags map[string]string) {
	delete(tags, tagRepositoryURL)
	delete(tags, tagCommitSha)
}

// GetTracerGitMetadataTags returns git metadata tags for tracer
// NB: Currently tracer inject tags with some workaround
// (only with _dd prefix and only for the first span in payload)
// So we provide different tag names
func GetTracerGitMetadataTags() map[string]string {
	res := make(map[string]string)
	tags := GetGitMetadataTags()

	repositoryURL := tags[tagRepositoryURL]
	commitSha := tags[tagCommitSha]

	if repositoryURL == "" || commitSha == "" {
		return res
	}

	res[traceTagCommitSha] = commitSha
	res[traceTagRepositoryURL] = repositoryURL
	return res
}

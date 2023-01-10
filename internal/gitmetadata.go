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
	EnvGitMetadataEnabledFlag = "DD_TRACE_GIT_METADATA_ENABLED"
	EnvGitRepositoryURL       = "DD_GIT_REPOSITORY_URL"
	EnvGitCommitSha           = "DD_GIT_COMMIT_SHA"
	EnvDDTags                 = "DD_TAGS"

	TagRepositoryURL = "git.repository_url"
	TagCommitSha     = "git.commit.sha"

	TraceTagRepositoryURL = "_dd.git.repository_url"
	TraceTagCommitSha     = "_dd.git.commit.sha"
)

var (
	lock = &sync.Mutex{}

	gitMetadataTags map[string]string
)

// Get git metadata from environment variables
func getTagsFromEnv() map[string]string {
	repositoryURL := os.Getenv(EnvGitRepositoryURL)
	commitSha := os.Getenv(EnvGitCommitSha)

	if repositoryURL == "" || commitSha == "" {
		tags := ParseTagString(os.Getenv(EnvDDTags))

		repositoryURL = tags[TagRepositoryURL]
		commitSha = tags[TagCommitSha]
	}

	if repositoryURL == "" || commitSha == "" {
		return nil
	}

	return map[string]string{
		TagRepositoryURL: repositoryURL,
		TagCommitSha:     commitSha,
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
	if BoolEnv(EnvGitMetadataEnabledFlag, true) {
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

// ResetGitMetadataTags reset cashed metadata tags
func ResetGitMetadataTags() {
	gitMetadataTags = nil
}

// CleanGitMetadataTags cleans up tags from git metadata
func CleanGitMetadataTags(tags map[string]string) {
	delete(tags, TagRepositoryURL)
	delete(tags, TagCommitSha)
}

// GetTracerGitMetadataTags returns git metadata tags for tracer
// NB: Currently tracer inject tags with some workaround
// (only with _dd prefix and only for the first span in payload)
// So we provide different tag names
func GetTracerGitMetadataTags() map[string]string {
	res := make(map[string]string)
	tags := GetGitMetadataTags()

	repositoryURL := tags[TagRepositoryURL]
	commitSha := tags[TagCommitSha]

	if repositoryURL == "" || commitSha == "" {
		return res
	}

	res[TraceTagCommitSha] = commitSha
	res[TraceTagRepositoryURL] = repositoryURL
	return res
}

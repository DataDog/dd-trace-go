// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package internal

const (
	// EnvGitMetadataEnabledFlag specifies the environment variable name for enable/disable
	EnvGitMetadataEnabledFlag = "DD_TRACE_GIT_METADATA_ENABLED"
	// EnvGitRepositoryURL specifies the environment variable name for git repository URL
	EnvGitRepositoryURL = "DD_GIT_REPOSITORY_URL"
	// EnvGitCommitSha specifies the environment variable name git commit sha
	EnvGitCommitSha = "DD_GIT_COMMIT_SHA"
	// EnvDDTags specifies the environment variable name global tags
	EnvDDTags = "DD_TAGS"

	// TagRepositoryURL specifies the tag name for git repository URL
	TagRepositoryURL = "git.repository_url"
	// TagCommitSha specifies the tag name for git commit sha
	TagCommitSha = "git.commit.sha"
	// TagGoPath specifies the tag name for go module path
	TagGoPath = "go_path"

	// TraceTagRepositoryURL specifies the trace tag name for git repository URL
	TraceTagRepositoryURL = "_dd.git.repository_url"
	// TraceTagCommitSha specifies the trace tag name for git commit sha
	TraceTagCommitSha = "_dd.git.commit.sha"
	// TraceTagGoPath specifies the trace tag name for go module path
	TraceTagGoPath = "_dd.go_path"
)

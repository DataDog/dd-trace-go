// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal

import "net/url"

// Exported variables whose purpose is to get injected at build-time using:
//
// go build -ldflags "-X gopkg.in/DataDog/dd-trace-go.v1/internal.gitRepositoryURL=$(git config --get remote.origin.url)
// -X gopkg.in/DataDog/dd-trace-go.v1/internal.gitCommitSha=$(git rev-parse HEAD)
// -X gopkg.in/DataDog/dd-trace-go.v1/internal.gitRepositoryBuildPath=$(git rev-parse --show-toplevel)"
var (
	gitRepositoryURL       string
	gitCommitSha           string
	gitRepositoryBuildPath string
)

// GitRepositoryURL returns the git remote url injected at build-time.
// Potential credentials are stripped from the URL.
func GitRepositoryURL() string {
	u, err := url.Parse(gitRepositoryURL)
	if err != nil {
		return gitRepositoryURL
	}
	u.User = nil
	return u.String()
}

// GitCommitSha returns the git commit injected at build-time.
func GitCommitSha() string {
	return gitCommitSha
}

// GitRepositoryBuildPath returns the git repository build path injected at build-time.
func GitRepositoryBuildPath() string {
	return gitRepositoryBuildPath
}

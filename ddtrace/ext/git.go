// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package ext

import (
	"os/exec"
	"strings"
)

const (
	// GitBranch indicates the current git branch.
	GitBranch = "git.branch"
	// GitCommitSHA indicates git commit SHA1 hash related to the build.
	GitCommitSHA = "git.commit.sha"
	// GitRepositoryURL indicates git repository URL related to the build.
	GitRepositoryURL = "git.repository_url"
	// GitTag indicates the current git tag.
	GitTag = "git.tag"
)

// LocalGitRepositoryURL detects Git repository URL using git command.
func LocalGitRepositoryURL() string {
	out, err := exec.Command("git", "ls-remote", "--get-url").Output()
	if err != nil {
		return ""
	}
	return strings.Trim(string(out), "\n")
}

// LocalGitBranch detects current Git branch using git command.
func LocalGitBranch() string {
	out, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.Trim(string(out), "\n")
}

// LocalGitCommitSHA detects SHA of a HEAD in Git repository.
func LocalGitCommitSHA() string {
	out, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.Trim(string(out), "\n")
}
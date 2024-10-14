// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package utils

import (
	"errors"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// localGitData holds various pieces of information about the local Git repository,
// including the source root, repository URL, branch, commit SHA, author and committer details, and commit message.
type localGitData struct {
	SourceRoot     string
	RepositoryURL  string
	Branch         string
	CommitSha      string
	AuthorDate     time.Time
	AuthorName     string
	AuthorEmail    string
	CommitterDate  time.Time
	CommitterName  string
	CommitterEmail string
	CommitMessage  string
}

var (
	// regexpSensitiveInfo is a regular expression used to match and filter out sensitive information from URLs.
	regexpSensitiveInfo = regexp.MustCompile("(https?://|ssh?://)[^/]*@")

	// isGitFoundValue is a boolean flag indicating whether the Git executable is available on the system.
	isGitFoundValue bool

	// gitFinder is a sync.Once instance used to ensure that the Git executable is only checked once.
	gitFinder sync.Once
)

// isGitFound checks if the Git executable is available on the system.
func isGitFound() bool {
	gitFinder.Do(func() {
		_, err := exec.LookPath("git")
		isGitFoundValue = err == nil
	})
	return isGitFoundValue
}

// execGit executes a Git command with the given arguments.
func execGit(args ...string) ([]byte, error) {
	if !isGitFound() {
		return nil, errors.New("git executable not found")
	}
	return exec.Command("git", args...).Output()
}

// execGitString executes a Git command with the given arguments and returns the output as a string.
func execGitString(args ...string) (string, error) {
	out, err := execGit(args...)
	if err != nil {
		return "", err
	}
	return strings.Trim(string(out), "\n"), err
}

// GetLocalGitData retrieves information about the local Git repository from the current HEAD.
// It gathers details such as the repository URL, current branch, latest commit SHA, author and committer details, and commit message.
//
// Returns:
//
//	A localGitData struct populated with the retrieved Git data.
//	An error if any Git command fails or the retrieved data is incomplete.
func GetLocalGitData() (localGitData, error) {
	gitData := localGitData{}

	if !isGitFound() {
		return gitData, errors.New("git executable not found")
	}

	// Extract the absolute path to the Git directory
	out, err := execGitString("rev-parse", "--absolute-git-dir")
	if err == nil {
		gitData.SourceRoot = strings.ReplaceAll(out, ".git", "")
	}

	// Extract the repository URL
	out, err = execGitString("ls-remote", "--get-url")
	if err == nil {
		gitData.RepositoryURL = filterSensitiveInfo(out)
	}

	// Extract the current branch name
	out, err = execGitString("rev-parse", "--abbrev-ref", "HEAD")
	if err == nil {
		gitData.Branch = out
	}

	// Get commit details from the latest commit using git log (git log -1 --pretty='%H","%aI","%an","%ae","%cI","%cn","%ce","%B')
	out, err = execGitString("log", "-1", "--pretty=%H\",\"%at\",\"%an\",\"%ae\",\"%ct\",\"%cn\",\"%ce\",\"%B")
	if err != nil {
		return gitData, err
	}

	// Split the output into individual components
	outArray := strings.Split(out, "\",\"")
	if len(outArray) < 8 {
		return gitData, errors.New("git log failed")
	}

	// Parse author and committer dates from Unix timestamp
	authorUnixDate, _ := strconv.ParseInt(outArray[1], 10, 64)
	committerUnixDate, _ := strconv.ParseInt(outArray[4], 10, 64)

	// Populate the localGitData struct with the parsed information
	gitData.CommitSha = outArray[0]
	gitData.AuthorDate = time.Unix(authorUnixDate, 0)
	gitData.AuthorName = outArray[2]
	gitData.AuthorEmail = outArray[3]
	gitData.CommitterDate = time.Unix(committerUnixDate, 0)
	gitData.CommitterName = outArray[5]
	gitData.CommitterEmail = outArray[6]
	gitData.CommitMessage = strings.Trim(outArray[7], "\n")
	return gitData, nil
}

func GetLastLocalGitCommitShas() []string {
	// git log --format=%H -n 1000 --since=\"1 month ago\"
	out, err := execGitString("log", "--format=%H", "-n", "1000", "--since=\"1 month ago\"")
	if err != nil {
		return []string{}
	}
	return strings.Split(out, "\n")
}

// filterSensitiveInfo removes sensitive information from a given URL using a regular expression.
// It replaces the user credentials part of the URL (if present) with an empty string.
//
// Parameters:
//
//	url - The URL string from which sensitive information should be filtered out.
//
// Returns:
//
//	The sanitized URL string with sensitive information removed.
func filterSensitiveInfo(url string) string {
	return string(regexpSensitiveInfo.ReplaceAll([]byte(url), []byte("$1"))[:])
}

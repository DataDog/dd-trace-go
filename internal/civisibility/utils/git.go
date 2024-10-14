// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package utils

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
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
		if err != nil {
			log.Debug("civisibility.git: git executable not found")
		}
	})
	return isGitFoundValue
}

// execGit executes a Git command with the given arguments.
func execGit(args ...string) ([]byte, error) {
	if !isGitFound() {
		return nil, errors.New("git executable not found")
	}
	return exec.Command("git", args...).CombinedOutput()
}

// execGitString executes a Git command with the given arguments and returns the output as a string.
func execGitString(args ...string) (string, error) {
	out, err := execGit(args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(strings.Trim(string(out), "\n")), err
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
	log.Debug("civisibility.git: getting the absolute path to the Git directory")
	out, err := execGitString("rev-parse", "--absolute-git-dir")
	if err == nil {
		gitData.SourceRoot = strings.ReplaceAll(out, ".git", "")
	}

	// Extract the repository URL
	log.Debug("civisibility.git: getting the repository URL")
	out, err = execGitString("ls-remote", "--get-url")
	if err == nil {
		gitData.RepositoryURL = filterSensitiveInfo(out)
	}

	// Extract the current branch name
	log.Debug("civisibility.git: getting the current branch name")
	out, err = execGitString("rev-parse", "--abbrev-ref", "HEAD")
	if err == nil {
		gitData.Branch = out
	}

	// Get commit details from the latest commit using git log (git log -1 --pretty='%H","%aI","%an","%ae","%cI","%cn","%ce","%B')
	log.Debug("civisibility.git: getting the latest commit details")
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

// GetLastLocalGitCommitShas retrieves the commit SHAs of the last 1000 commits in the local Git repository.
func GetLastLocalGitCommitShas() []string {
	// git log --format=%H -n 1000 --since=\"1 month ago\"
	log.Debug("civisibility.git: getting the commit SHAs of the last 1000 commits in the local Git repository")
	out, err := execGitString("log", "--format=%H", "-n", "1000", "--since=\"1 month ago\"")
	if err != nil {
		return []string{}
	}
	return strings.Split(out, "\n")
}

// UnshallowGitRepository converts a shallow clone into a complete clone by fetching all missing commits without git content (only commits and tree objects).
func UnshallowGitRepository() (bool, error) {

	// let's do a first check to see if the repository is a shallow clone
	log.Debug("civisibility.unshallow: checking if the repository is a shallow clone")
	isAShallowClone, err := isAShallowCloneRepository()
	if err != nil {
		log.Debug("civisibility.unshallow: error checking if the repository is a shallow clone: %v", err)
		return false, errors.New(fmt.Sprintf("civisibility.unshallow: error checking if the repository is a shallow clone: %v", err))
	}

	// if the git repo is not a shallow clone, we can return early
	if !isAShallowClone {
		log.Debug("civisibility.unshallow: the repository is not a shallow clone")
		return true, nil
	}

	// the git repo is a shallow clone, we need to double check if there are more than just 1 commit in the logs.
	log.Debug("civisibility.unshallow: the repository is a shallow clone, checking if there are more than 2 commits in the logs")
	hasMoreThanTwoCommits, err := hasTheGitLogHaveMoreThanTwoCommits()
	if err != nil {
		log.Debug("civisibility.unshallow: error checking if the git log has more than two commits: %v", err)
		return false, errors.New(fmt.Sprintf("civisibility.unshallow: error checking if the git log has more than two commits: %v", err))
	}

	// if there are more than 2 commits, we can return early
	if hasMoreThanTwoCommits {
		log.Debug("civisibility.unshallow: the git log has more than two commits")
		return true, nil
	}

	// after asking for 2 logs lines, if the git log command returns just one commit sha, we reconfigure the repo
	// to ask for git commits and trees of the last month (no blobs)

	// let's get the origin name (git config --default origin --get clone.defaultRemoteName)
	originName, err := execGitString("config", "--default", "origin", "--get", "clone.defaultRemoteName")
	if err != nil {
		log.Debug("civisibility.unshallow: error getting the origin name: %v", err)
		return false, errors.New(fmt.Sprintf("civisibility.unshallow: error getting the origin name: %v\n%s", err, originName))
	}
	if originName == "" {
		// if the origin name is empty, we fallback to "origin"
		originName = "origin"
	}
	log.Debug("civisibility.unshallow: origin name: %v", originName)

	// let's get the sha of the HEAD (git rev-parse HEAD)
	headSha, err := execGitString("rev-parse", "HEAD")
	if err != nil {
		log.Debug("civisibility.unshallow: error getting the HEAD sha: %v", err)
		return false, errors.New(fmt.Sprintf("civisibility.unshallow: error getting the HEAD sha: %v\n%s", err, headSha))
	}
	if headSha == "" {
		// if the HEAD is empty, we fallback to the current branch (git branch --show-current)
		headSha, err = execGitString("branch", "--show-current")
		if err != nil {
			log.Debug("civisibility.unshallow: error getting the current branch: %v", err)
			return false, errors.New(fmt.Sprintf("civisibility.unshallow: error getting the current branch: %v\n%s", err, headSha))
		}
	}
	log.Debug("civisibility.unshallow: HEAD sha: %v", headSha)

	// let's fetch the missing commits and trees from the last month
	// git fetch --shallow-since="1 month ago" --update-shallow --filter="blob:none" --recurse-submodules=no $(git config --default origin --get clone.defaultRemoteName) $(git rev-parse HEAD)
	log.Debug("civisibility.unshallow: fetching the missing commits and trees from the last month")
	fetchOutput, err := execGitString("fetch", "--shallow-since=\"1 month ago\"", "--update-shallow", "--filter=\"blob:none\"", "--recurse-submodules=no", originName, headSha)

	// let's check if the last command was unsuccessful
	if err != nil || fetchOutput == "" {
		log.Debug("civisibility.unshallow: error fetching the missing commits and trees from the last month: %v", err)
		// ***
		// The previous command has a drawback: if the local HEAD is a commit that has not been pushed to the remote, it will fail.
		// If this is the case, we fallback to: `git fetch --shallow-since="1 month ago" --update-shallow --filter="blob:none" --recurse-submodules=no $(git config --default origin --get clone.defaultRemoteName) $(git rev-parse --abbrev-ref --symbolic-full-name @{upstream})`
		// This command will attempt to use the tracked branch for the current branch in order to unshallow.
		// ***

		// let's get the remote branch name: git rev-parse --abbrev-ref --symbolic-full-name @{upstream}
		var remoteBranchName string
		log.Debug("civisibility.unshallow: getting the remote branch name")
		remoteBranchName, err = execGitString("rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
		if err == nil {
			// let's try the alternative: git fetch --shallow-since="1 month ago" --update-shallow --filter="blob:none" --recurse-submodules=no $(git config --default origin --get clone.defaultRemoteName) $(git rev-parse --abbrev-ref --symbolic-full-name @{upstream})
			log.Debug("civisibility.unshallow: fetching the missing commits and trees from the last month using the remote branch name")
			fetchOutput, err = execGitString("fetch", "--shallow-since=\"1 month ago\"", "--update-shallow", "--filter=\"blob:none\"", "--recurse-submodules=no", originName, remoteBranchName)
		}
	}

	// let's check if the last command was unsuccessful
	if err != nil || fetchOutput == "" {
		log.Debug("civisibility.unshallow: error fetching the missing commits and trees from the last month: %v", err)
		// ***
		// It could be that the CI is working on a detached HEAD or maybe branch tracking hasn’t been set up.
		// In that case, this command will also fail, and we will finally fallback to we just unshallow all the things:
		// `git fetch --shallow-since="1 month ago" --update-shallow --filter="blob:none" --recurse-submodules=no $(git config --default origin --get clone.defaultRemoteName)`
		// ***

		// let's try the last fallback: git fetch --shallow-since="1 month ago" --update-shallow --filter="blob:none" --recurse-submodules=no $(git config --default origin --get clone.defaultRemoteName)
		log.Debug("civisibility.unshallow: fetching the missing commits and trees from the last month using the origin name")
		fetchOutput, err = execGitString("fetch", "--shallow-since=\"1 month ago\"", "--update-shallow", "--filter=\"blob:none\"", "--recurse-submodules=no", originName)
	}

	if err != nil {
		log.Debug("civisibility.unshallow: error: %v", err)
		return false, errors.New(fmt.Sprintf("civisibility.unshallow: error: %v\n%s", err, fetchOutput))
	}

	log.Debug("civisibility.unshallow: was completed successfully")
	return true, nil
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

// isAShallowCloneRepository checks if the local Git repository is a shallow clone.
func isAShallowCloneRepository() (bool, error) {
	// git rev-parse --is-shallow-repository
	out, err := execGitString("rev-parse", "--is-shallow-repository")
	if err != nil {
		return false, err
	}

	return strings.TrimSpace(out) == "true", nil
}

// hasTheGitLogHaveMoreThanTwoCommits checks if the local Git repository has more than two commits.
func hasTheGitLogHaveMoreThanTwoCommits() (bool, error) {
	// git log --format=oneline -n 2
	out, err := execGitString("log", "--format=oneline", "-n", "2")
	if err != nil {
		return false, err
	}

	commitsCount := strings.Count(out, "\n") + 1
	return commitsCount > 1, nil
}

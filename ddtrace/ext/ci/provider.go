// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package ci

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	homedir "github.com/mitchellh/go-homedir"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
)

type providerType = func() map[string]string

var providers = map[string]providerType{
	"APPVEYOR":           extractAppveyor,
	"TF_BUILD":           extractAzurePipelines,
	"BITBUCKET_COMMIT":   extractBitbucket,
	"BUILDKITE":          extractBuildkite,
	"CIRCLECI":           extractCircleCI,
	"GITHUB_SHA":         extractGithubActions,
	"GITLAB_CI":          extractGitlab,
	"JENKINS_URL":        extractJenkins,
	"TEAMCITY_VERSION":   extractTeamcity,
	"TRAVIS":             extractTravis,
	"BITRISE_BUILD_SLUG": extractBitrise,
}

// Tags extracts CI information from environment variables.
func Tags() map[string]string {
	tags := map[string]string{}
	for key, provider := range providers {
		if _, ok := os.LookupEnv(key); !ok {
			continue
		}
		tags = provider()

		if tag, ok := tags[ext.GitTag]; ok && tag != "" {
			tags[ext.GitTag] = normalizeRef(tag)
			delete(tags, ext.GitBranch)
		}
		if tag, ok := tags[ext.GitBranch]; ok && tag != "" {
			tags[ext.GitBranch] = normalizeRef(tag)
		}
		if tag, ok := tags[ext.GitRepositoryURL]; ok && tag != "" {
			tags[ext.GitRepositoryURL] = filterSensitiveInfo(tag)
		}

		// Expand ~
		if tag, ok := tags[ext.CIWorkspacePath]; ok && tag != "" {
			homedir.Reset()
			if value, err := homedir.Expand(tag); err == nil {
				tags[ext.CIWorkspacePath] = value
			}
		}

		// remove empty values
		for tag, value := range tags {
			if value == "" {
				delete(tags, tag)
			}
		}
	}
	return tags
}

func normalizeRef(name string) string {
	empty := []byte("")
	refs := regexp.MustCompile("^refs/(heads/)?")
	origin := regexp.MustCompile("^origin/")
	tags := regexp.MustCompile("^tags/")
	return string(tags.ReplaceAll(origin.ReplaceAll(refs.ReplaceAll([]byte(name), empty), empty), empty)[:])
}

func filterSensitiveInfo(url string) string {
	return string(regexp.MustCompile("(https?://)[^/]*@").ReplaceAll([]byte(url), []byte("$1"))[:])
}

func lookupEnvs(keys ...string) ([]string, bool) {
	values := make([]string, len(keys))
	for _, key := range keys {
		value, ok := os.LookupEnv(key)
		if !ok {
			return nil, false
		}
		values = append(values, value)
	}
	return values, true
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value, ok := os.LookupEnv(key); ok {
			return value
		}
	}
	return ""
}

func extractAppveyor() map[string]string {
	tags := map[string]string{}
	url := fmt.Sprintf("https://ci.appveyor.com/project/%s/builds/%s", os.Getenv("APPVEYOR_REPO_NAME"), os.Getenv("APPVEYOR_BUILD_ID"))
	tags[ext.CIProviderName] = "appveyor"
	if os.Getenv("APPVEYOR_REPO_PROVIDER") == "github" {
		tags[ext.GitRepositoryURL] = fmt.Sprintf("https://github.com/%s.git", os.Getenv("APPVEYOR_REPO_NAME"))
		tags[ext.GitCommitSHA] = os.Getenv("APPVEYOR_REPO_COMMIT")
		tags[ext.GitBranch] = firstEnv("APPVEYOR_PULL_REQUEST_HEAD_REPO_BRANCH", "APPVEYOR_REPO_BRANCH")
		tags[ext.GitTag] = os.Getenv("APPVEYOR_REPO_TAG_NAME")
	}
	tags[ext.CIWorkspacePath] = os.Getenv("APPVEYOR_BUILD_FOLDER")
	tags[ext.CIPipelineID] = os.Getenv("APPVEYOR_BUILD_ID")
	tags[ext.CIPipelineName] = os.Getenv("APPVEYOR_REPO_NAME")
	tags[ext.CIPipelineNumber] = os.Getenv("APPVEYOR_BUILD_NUMBER")
	tags[ext.CIPipelineURL] = url
	tags[ext.CIJobURL] = url
	return tags
}

func extractAzurePipelines() map[string]string {
	tags := map[string]string{}
	baseURL := fmt.Sprintf("%s%s/_build/results?buildId=%s", os.Getenv("SYSTEM_TEAMFOUNDATIONSERVERURI"), os.Getenv("SYSTEM_TEAMPROJECTID"), os.Getenv("BUILD_BUILDID"))
	pipelineURL := baseURL
	jobURL := fmt.Sprintf("%s&view=logs&j=%s&t=%s", baseURL, os.Getenv("SYSTEM_JOBID"), os.Getenv("SYSTEM_TASKINSTANCEID"))
	branchOrTag := firstEnv("SYSTEM_PULLREQUEST_SOURCEBRANCH", "BUILD_SOURCEBRANCH", "BUILD_SOURCEBRANCHNAME")
	branch := ""
	tag := ""
	if strings.Contains(branchOrTag, "tags/") {
		tag = branchOrTag
	} else {
		branch = branchOrTag
	}
	tags[ext.CIProviderName] = "azurepipelines"
	tags[ext.CIWorkspacePath] = os.Getenv("BUILD_SOURCESDIRECTORY")
	tags[ext.CIPipelineID] = os.Getenv("BUILD_BUILDID")
	tags[ext.CIPipelineName] = os.Getenv("BUILD_DEFINITIONNAME")
	tags[ext.CIPipelineNumber] = os.Getenv("BUILD_BUILDID")
	tags[ext.CIPipelineURL] = pipelineURL
	tags[ext.CIJobURL] = jobURL
	tags[ext.GitRepositoryURL] = firstEnv("SYSTEM_PULLREQUEST_SOURCEREPOSITORYURI", "BUILD_REPOSITORY_URI")
	tags[ext.GitCommitSHA] = firstEnv("SYSTEM_PULLREQUEST_SOURCECOMMITID", "BUILD_SOURCEVERSION")
	tags[ext.GitBranch] = branch
	tags[ext.GitTag] = tag
	return tags
}

func extractBitbucket() map[string]string {
	tags := map[string]string{}
	url := fmt.Sprintf("https://bitbucket.org/%s/addon/pipelines/home#!/results/%s", os.Getenv("BITBUCKET_REPO_FULL_NAME"), os.Getenv("BITBUCKET_BUILD_NUMBER"))
	tags[ext.GitBranch] = os.Getenv("BITBUCKET_BRANCH")
	tags[ext.GitCommitSHA] = os.Getenv("BITBUCKET_COMMIT")
	tags[ext.GitRepositoryURL] = os.Getenv("BITBUCKET_GIT_SSH_ORIGIN")
	tags[ext.GitTag] = os.Getenv("BITBUCKET_TAG")
	tags[ext.CIJobURL] = url
	tags[ext.CIPipelineID] = strings.Trim(os.Getenv("BITBUCKET_PIPELINE_UUID"), "{}")
	tags[ext.CIPipelineName] = os.Getenv("BITBUCKET_REPO_FULL_NAME")
	tags[ext.CIPipelineNumber] = os.Getenv("BITBUCKET_BUILD_NUMBER")
	tags[ext.CIPipelineURL] = url
	tags[ext.CIProviderName] = "bitbucket"
	tags[ext.CIWorkspacePath] = os.Getenv("BITBUCKET_CLONE_DIR")
	return tags
}

func extractBuildkite() map[string]string {
	tags := map[string]string{}
	tags[ext.GitBranch] = os.Getenv("BUILDKITE_BRANCH")
	tags[ext.GitCommitSHA] = os.Getenv("BUILDKITE_COMMIT")
	tags[ext.GitRepositoryURL] = os.Getenv("BUILDKITE_REPO")
	tags[ext.GitTag] = os.Getenv("BUILDKITE_TAG")
	tags[ext.CIPipelineID] = os.Getenv("BUILDKITE_BUILD_ID")
	tags[ext.CIPipelineName] = os.Getenv("BUILDKITE_PIPELINE_SLUG")
	tags[ext.CIPipelineNumber] = os.Getenv("BUILDKITE_BUILD_NUMBER")
	tags[ext.CIPipelineURL] = os.Getenv("BUILDKITE_BUILD_URL")
	tags[ext.CIJobURL] = fmt.Sprintf("%s#%s", os.Getenv("BUILDKITE_BUILD_URL"), os.Getenv("BUILDKITE_JOB_ID"))
	tags[ext.CIProviderName] = "buildkite"
	tags[ext.CIWorkspacePath] = os.Getenv("BUILDKITE_BUILD_CHECKOUT_PATH")
	return tags
}

func extractCircleCI() map[string]string {
	tags := map[string]string{}
	tags[ext.GitBranch] = os.Getenv("CIRCLE_BRANCH")
	tags[ext.GitCommitSHA] = os.Getenv("CIRCLE_SHA1")
	tags[ext.GitRepositoryURL] = os.Getenv("CIRCLE_REPOSITORY_URL")
	tags[ext.GitTag] = os.Getenv("CIRCLE_TAG")
	tags[ext.CIPipelineID] = os.Getenv("CIRCLE_WORKFLOW_ID")
	tags[ext.CIPipelineName] = os.Getenv("CIRCLE_PROJECT_REPONAME")
	tags[ext.CIPipelineNumber] = os.Getenv("CIRCLE_BUILD_NUM")
	tags[ext.CIPipelineURL] = os.Getenv("CIRCLE_BUILD_URL")
	tags[ext.CIJobURL] = os.Getenv("CIRCLE_BUILD_URL")
	tags[ext.CIProviderName] = "circleci"
	tags[ext.CIWorkspacePath] = os.Getenv("CIRCLE_WORKING_DIRECTORY")
	return tags
}

func extractGithubActions() map[string]string {
	tags := map[string]string{}
	branchOrTag := firstEnv("GITHUB_HEAD_REF", "GITHUB_REF")
	tag := ""
	branch := ""
	if strings.Contains(branchOrTag, "tags/") {
		tag = branchOrTag
	} else {
		branch = branchOrTag

	}

	tags[ext.GitBranch] = branch
	tags[ext.GitCommitSHA] = os.Getenv("GITHUB_SHA")
	tags[ext.GitRepositoryURL] = fmt.Sprintf("https://github.com/%s.git", os.Getenv("GITHUB_REPOSITORY"))
	tags[ext.GitTag] = tag
	tags[ext.CIJobURL] = fmt.Sprintf("https://github.com/%s/commit/%s/checks", os.Getenv("GITHUB_REPOSITORY"), os.Getenv("GITHUB_SHA"))
	tags[ext.CIPipelineID] = os.Getenv("GITHUB_RUN_ID")
	tags[ext.CIPipelineName] = os.Getenv("GITHUB_WORKFLOW")
	tags[ext.CIPipelineNumber] = os.Getenv("GITHUB_RUN_NUMBER")
	tags[ext.CIPipelineURL] = fmt.Sprintf("https://github.com/%s/commit/%s/checks", os.Getenv("GITHUB_REPOSITORY"), os.Getenv("GITHUB_SHA"))
	tags[ext.CIProviderName] = "github"
	tags[ext.CIWorkspacePath] = os.Getenv("GITHUB_WORKSPACE")
	return tags
}

func extractGitlab() map[string]string {
	tags := map[string]string{}
	url := os.Getenv("CI_PIPELINE_URL")
	url = string(regexp.MustCompile("/-/pipelines/").ReplaceAll([]byte(url), []byte("/pipelines/"))[:])
	tags[ext.GitBranch] = os.Getenv("CI_COMMIT_BRANCH")
	tags[ext.GitCommitSHA] = os.Getenv("CI_COMMIT_SHA")
	tags[ext.GitRepositoryURL] = os.Getenv("CI_REPOSITORY_URL")
	tags[ext.GitTag] = os.Getenv("CI_COMMIT_TAG")
	tags[ext.CIStageName] = os.Getenv("CI_JOB_STAGE")
	tags[ext.CIJobName] = os.Getenv("CI_JOB_NAME")
	tags[ext.CIJobURL] = os.Getenv("CI_JOB_URL")
	tags[ext.CIPipelineID] = os.Getenv("CI_PIPELINE_ID")
	tags[ext.CIPipelineName] = os.Getenv("CI_PROJECT_PATH")
	tags[ext.CIPipelineNumber] = os.Getenv("CI_PIPELINE_IID")
	tags[ext.CIPipelineURL] = url
	tags[ext.CIProviderName] = "gitlab"
	tags[ext.CIWorkspacePath] = os.Getenv("CI_PROJECT_DIR")
	return tags
}

func extractJenkins() map[string]string {
	tags := map[string]string{}
	branchOrTag := os.Getenv("GIT_BRANCH")
	empty := []byte("")
	name, hasName := os.LookupEnv("JOB_NAME")

	if strings.Contains(branchOrTag, "tags/") {
		tags[ext.GitTag] = branchOrTag
	} else {
		tags[ext.GitBranch] = branchOrTag
		// remove branch for job name
		removeBranch := regexp.MustCompile(fmt.Sprintf("/%s", normalizeRef(branchOrTag)))
		name = string(removeBranch.ReplaceAll([]byte(name), empty))
	}

	if hasName {
		removeVars := regexp.MustCompile("/[^/]+=[^/]*")
		name = string(removeVars.ReplaceAll([]byte(name), empty))
	}

	tags[ext.GitCommitSHA] = os.Getenv("GIT_COMMIT")
	tags[ext.GitRepositoryURL] = os.Getenv("GIT_URL")
	tags[ext.CIPipelineID] = os.Getenv("BUILD_TAG")
	tags[ext.CIPipelineName] = name
	tags[ext.CIPipelineNumber] = os.Getenv("BUILD_NUMBER")
	tags[ext.CIPipelineURL] = os.Getenv("BUILD_URL")
	tags[ext.CIProviderName] = "jenkins"
	tags[ext.CIWorkspacePath] = os.Getenv("WORKSPACE")
	return tags
}

func extractTeamcity() map[string]string {
	tags := map[string]string{}
	tags[ext.CIProviderName] = "teamcity"
	tags[ext.GitRepositoryURL] = os.Getenv("BUILD_VCS_URL")
	tags[ext.GitCommitSHA] = os.Getenv("BUILD_VCS_NUMBER")
	tags[ext.CIWorkspacePath] = os.Getenv("BUILD_CHECKOUTDIR")
	tags[ext.CIPipelineID] = os.Getenv("BUILD_ID")
	tags[ext.CIPipelineNumber] = os.Getenv("BUILD_NUMBER")
	tags[ext.CIPipelineURL] = fmt.Sprintf("%s/viewLog.html?buildId=%s", os.Getenv("SERVER_URL"), os.Getenv("SERVER_URL"))
	return tags
}

func extractTravis() map[string]string {
	tags := map[string]string{}
	tags[ext.GitBranch] = firstEnv("TRAVIS_PULL_REQUEST_BRANCH", "TRAVIS_BRANCH")
	tags[ext.GitCommitSHA] = os.Getenv("TRAVIS_COMMIT")
	tags[ext.GitRepositoryURL] = fmt.Sprintf("https://github.com/%s.git", os.Getenv("TRAVIS_REPO_SLUG"))
	tags[ext.GitTag] = os.Getenv("TRAVIS_TAG")
	tags[ext.CIJobURL] = os.Getenv("TRAVIS_JOB_WEB_URL")
	tags[ext.CIPipelineID] = os.Getenv("TRAVIS_BUILD_ID")
	tags[ext.CIPipelineName] = os.Getenv("TRAVIS_REPO_SLUG")
	tags[ext.CIPipelineNumber] = os.Getenv("TRAVIS_BUILD_NUMBER")
	tags[ext.CIPipelineURL] = os.Getenv("TRAVIS_BUILD_WEB_URL")
	tags[ext.CIProviderName] = "travisci"
	tags[ext.CIWorkspacePath] = os.Getenv("TRAVIS_BUILD_DIR")
	return tags
}

func extractBitrise() map[string]string {
	tags := map[string]string{}
	tags[ext.CIProviderName] = "bitrise"
	tags[ext.CIPipelineID] = os.Getenv("BITRISE_BUILD_SLUG")
	tags[ext.CIPipelineName] = os.Getenv("BITRISE_APP_TITLE")
	tags[ext.CIPipelineNumber] = os.Getenv("BITRISE_BUILD_NUMBER")
	tags[ext.CIPipelineURL] = os.Getenv("BITRISE_BUILD_URL")
	tags[ext.CIWorkspacePath] = os.Getenv("BITRISE_SOURCE_DIR")
	tags[ext.GitRepositoryURL] = os.Getenv("GIT_REPOSITORY_URL")
	tags[ext.GitCommitSHA] = firstEnv("BITRISE_GIT_COMMIT", "GIT_CLONE_COMMIT_HASH")
	tags[ext.GitBranch] = firstEnv("BITRISEIO_GIT_BRANCH_DEST", "BITRISE_GIT_BRANCH")
	tags[ext.GitTag] = os.Getenv("BITRISE_GIT_TAG")
	return tags
}

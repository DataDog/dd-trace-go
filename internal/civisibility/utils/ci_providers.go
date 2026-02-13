// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package utils

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// GitHub Actions job ID resolution constants and helpers
const (
	// githubJobCheckRunIDEnv is the environment variable name for the numeric job ID
	githubJobCheckRunIDEnv = "JOB_CHECK_RUN_ID"
	// githubMaxDiagFileSize is the maximum file size to read from diagnostics files (10MB)
	githubMaxDiagFileSize = 10 * 1024 * 1024
)

// githubActionsDiagnosticsEnabled controls whether diagnostics file scanning is enabled.
// This can be set to false in tests to prevent scanning real _diag directories.
var githubActionsDiagnosticsEnabled = true

// githubActionsDiagDirsLinux contains the possible diagnostics directories on Linux
var githubActionsDiagDirsLinux = []string{
	"/home/runner/actions-runner/cached/_diag",
	"/home/runner/actions-runner/_diag",
}

// githubActionsDiagDirsDarwin contains the possible diagnostics directories on macOS
var githubActionsDiagDirsDarwin = []string{
	"/Users/runner/actions-runner/cached/_diag",
	"/Users/runner/actions-runner/_diag",
}

// githubCheckRunIDRegex is used to extract the check_run_id from Worker log files
var githubCheckRunIDRegex = regexp.MustCompile(`"k"\s*:\s*"check_run_id"\s*,\s*"v"\s*:\s*([0-9]+)(?:\.0)?`)

// diagJobData represents the JSON structure of GitHub Actions diagnostics files
type diagJobData struct {
	Job struct {
		D []struct {
			K string      `json:"k"`
			V interface{} `json:"v"`
		} `json:"d"`
	} `json:"job"`
}

// getGithubActionsJobID returns the numeric job ID for GitHub Actions.
// It first checks the JOB_CHECK_RUN_ID environment variable, then falls back
// to reading the job ID from GitHub Actions diagnostics files.
// Only returns valid numeric job IDs; non-numeric values are treated as not found.
func getGithubActionsJobID() string {
	// Priority 1: Environment variable (only if numeric)
	if jobID := strings.TrimSpace(env.Get(githubJobCheckRunIDEnv)); jobID != "" && isNumericJobID(jobID) {
		return jobID
	}

	// Priority 2: Diagnostics files (can be disabled in tests)
	if githubActionsDiagnosticsEnabled {
		if jobID, ok := tryExtractJobIDFromDiag(getGithubActionsDiagDirs()); ok {
			return jobID
		}
	}

	return ""
}

// getGithubActionsDiagDirs returns the OS-specific diagnostics directory paths.
func getGithubActionsDiagDirs() []string {
	switch runtime.GOOS {
	case "windows":
		var candidates []string
		// Only add paths with ProgramFiles if the env var is set (avoid relative paths)
		//nolint:forbidigo
		if programFiles := os.Getenv("ProgramFiles"); programFiles != "" {
			candidates = append(candidates,
				filepath.Join(programFiles, "actions-runner", "cached", "_diag"),
				filepath.Join(programFiles, "actions-runner", "_diag"),
			)
		}
		//nolint:forbidigo
		if programFilesX86 := os.Getenv("ProgramFiles(x86)"); programFilesX86 != "" {
			candidates = append(candidates,
				filepath.Join(programFilesX86, "actions-runner", "cached", "_diag"),
				filepath.Join(programFilesX86, "actions-runner", "_diag"),
			)
		}
		// Always include hardcoded fallback paths
		candidates = append(candidates,
			`C:\actions-runner\cached\_diag`,
			`C:\actions-runner\_diag`,
		)
		return deduplicatePaths(candidates)
	case "darwin":
		return githubActionsDiagDirsDarwin
	default:
		return githubActionsDiagDirsLinux
	}
}

// deduplicatePaths removes empty and duplicate paths from the slice.
func deduplicatePaths(paths []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// tryExtractJobIDFromDiag attempts to extract the job ID from GitHub Actions diagnostics files.
// It scans Worker_*.log files in the diagnostics directories, sorted by modification time (newest first).
func tryExtractJobIDFromDiag(diagDirs []string) (string, bool) {
	for _, diagDir := range diagDirs {
		// Check if directory exists
		if info, err := os.Stat(diagDir); err != nil || !info.IsDir() {
			continue
		}

		// Find Worker_*.log files
		files, err := filepath.Glob(filepath.Join(diagDir, "Worker_*.log"))
		if err != nil {
			log.Debug("civisibility: error globbing worker logs in %s: %s", diagDir, err.Error())
			continue
		}

		if len(files) == 0 {
			continue
		}

		// Sort by modification time (newest first)
		sort.Slice(files, func(i, j int) bool {
			iInfo, _ := os.Stat(files[i])
			jInfo, _ := os.Stat(files[j])
			if iInfo == nil || jInfo == nil {
				return false
			}
			return iInfo.ModTime().After(jInfo.ModTime())
		})

		// Try to extract job ID from each file
		for _, file := range files {
			if jobID, ok := tryExtractJobIDFromFile(file); ok {
				return jobID, true
			}
		}
	}

	return "", false
}

// tryExtractJobIDFromFile attempts to extract the job ID from a single Worker log file.
// It first tries JSON parsing, then falls back to regex extraction.
func tryExtractJobIDFromFile(path string) (string, bool) {
	// Check file size before reading
	info, err := os.Stat(path)
	if err != nil {
		log.Debug("civisibility: error stating file %s: %s", path, err.Error())
		return "", false
	}
	if info.Size() > githubMaxDiagFileSize {
		log.Debug("civisibility: skipping oversized diagnostics file %s (%d bytes)", path, info.Size())
		return "", false
	}

	// Read file content
	content, err := os.ReadFile(path)
	if err != nil {
		log.Debug("civisibility: error reading file %s: %s", path, err.Error())
		return "", false
	}

	// Try JSON parsing first
	if jobID, ok := tryExtractJobIDFromJSON(content); ok {
		log.Debug("civisibility: extracted github actions job id via JSON: %s from %s", jobID, path)
		return jobID, true
	}

	// Fall back to regex extraction
	if jobID, ok := tryExtractJobIDFromRegex(content); ok {
		log.Debug("civisibility: extracted github actions job id via regex: %s from %s", jobID, path)
		return jobID, true
	}

	return "", false
}

// tryExtractJobIDFromJSON attempts to parse the content as JSON and extract the check_run_id.
func tryExtractJobIDFromJSON(content []byte) (string, bool) {
	var data diagJobData
	if err := json.Unmarshal(content, &data); err != nil {
		return "", false
	}

	for _, item := range data.Job.D {
		if item.K == "check_run_id" {
			var jobID string
			switch v := item.V.(type) {
			case float64:
				// Reject non-integer floats (e.g., 12345.5)
				if v != float64(int64(v)) {
					continue
				}
				jobID = strconv.FormatFloat(v, 'f', 0, 64)
			case string:
				jobID = v
			case json.Number:
				jobID = v.String()
			default:
				continue
			}
			jobID = strings.TrimSpace(jobID)
			if jobID != "" && isNumericJobID(jobID) {
				return jobID, true
			}
		}
	}

	return "", false
}

// tryExtractJobIDFromRegex attempts to extract the check_run_id using regex.
func tryExtractJobIDFromRegex(content []byte) (string, bool) {
	matches := githubCheckRunIDRegex.FindSubmatch(content)
	if len(matches) >= 2 {
		jobID := strings.TrimSpace(string(matches[1]))
		if jobID != "" && isNumericJobID(jobID) {
			return jobID, true
		}
	}
	return "", false
}

// isNumericJobID validates that the job ID contains only digits.
func isNumericJobID(id string) bool {
	if id == "" {
		return false
	}
	for _, c := range id {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// providerType defines a function type that returns a map of string key-value pairs.
type providerType = func() map[string]string

// providers maps environment variable names to their corresponding CI provider extraction functions.
var providers = map[string]providerType{
	"APPVEYOR":            extractAppveyor,
	"TF_BUILD":            extractAzurePipelines,
	"BITBUCKET_COMMIT":    extractBitbucket,
	"BUDDY":               extractBuddy,
	"BUILDKITE":           extractBuildkite,
	"CIRCLECI":            extractCircleCI,
	"GITHUB_SHA":          extractGithubActions,
	"GITLAB_CI":           extractGitlab,
	"JENKINS_URL":         extractJenkins,
	"TEAMCITY_VERSION":    extractTeamcity,
	"TRAVIS":              extractTravis,
	"BITRISE_BUILD_SLUG":  extractBitrise,
	"CF_BUILD_ID":         extractCodefresh,
	"CODEBUILD_INITIATOR": extractAwsCodePipeline,
	"DRONE":               extractDrone,
}

// getEnvVarsJSON returns a JSON representation of the specified environment variables.
func getEnvVarsJSON(envVars ...string) ([]byte, error) {
	envVarsMap := make(map[string]string)
	for _, envVar := range envVars {
		value := env.Get(envVar)
		if value != "" {
			envVarsMap[envVar] = value
		}
	}
	return json.Marshal(envVarsMap)
}

// getProviderTags extracts CI information from environment variables.
func getProviderTags() map[string]string {
	tags := map[string]string{}
	for key, provider := range providers {
		if _, ok := env.Lookup(key); !ok {
			continue
		}
		tags = provider()
	}

	// replace with user specific tags
	replaceWithUserSpecificTags(tags)

	// Normalize tags
	normalizeTags(tags)

	// Expand ~
	if tag, ok := tags[constants.CIWorkspacePath]; ok && tag != "" {
		tags[constants.CIWorkspacePath] = ExpandPath(tag)
	}

	// remove empty values
	for tag, value := range tags {
		if value == "" {
			delete(tags, tag)
		}
	}

	if log.DebugEnabled() {
		if providerName, ok := tags[constants.CIProviderName]; ok {
			log.Debug("civisibility: detected ci provider: %s", providerName)
		} else {
			log.Debug("civisibility: no ci provider was detected.")
		}
	}

	return tags
}

// normalizeTags normalizes specific tags to remove prefixes and sensitive information.
func normalizeTags(tags map[string]string) {
	if tag, ok := tags[constants.GitBranch]; ok && tag != "" {
		if strings.Contains(tag, "refs/tags") || strings.Contains(tag, "origin/tags") || strings.Contains(tag, "refs/heads/tags") {
			tags[constants.GitTag] = normalizeRef(tag)
		}
		tags[constants.GitBranch] = normalizeRef(tag)
	}
	if tag, ok := tags[constants.GitTag]; ok && tag != "" {
		tags[constants.GitTag] = normalizeRef(tag)
	}
	if tag, ok := tags[constants.GitPrBaseBranch]; ok && tag != "" {
		tags[constants.GitPrBaseBranch] = normalizeRef(tag)
	}
	if tag, ok := tags[constants.GitRepositoryURL]; ok && tag != "" {
		tags[constants.GitRepositoryURL] = filterSensitiveInfo(tag)
	}
	if tag, ok := tags[constants.CIPipelineURL]; ok && tag != "" {
		tags[constants.CIPipelineURL] = filterSensitiveInfo(tag)
	}
	if tag, ok := tags[constants.CIJobURL]; ok && tag != "" {
		tags[constants.CIJobURL] = filterSensitiveInfo(tag)
	}
	if tag, ok := tags[constants.CIEnvVars]; ok && tag != "" {
		tags[constants.CIEnvVars] = filterSensitiveInfo(tag)
	}
}

// replaceWithUserSpecificTags replaces certain tags with user-specific environment variable values.
func replaceWithUserSpecificTags(tags map[string]string) {
	replace := func(tagName, envName string) {
		tags[tagName] = getEnvironmentVariableIfIsNotEmpty(envName, tags[tagName])
	}

	replace(constants.GitBranch, "DD_GIT_BRANCH")
	replace(constants.GitTag, "DD_GIT_TAG")
	replace(constants.GitRepositoryURL, "DD_GIT_REPOSITORY_URL")
	replace(constants.GitCommitSHA, "DD_GIT_COMMIT_SHA")
	replace(constants.GitCommitMessage, "DD_GIT_COMMIT_MESSAGE")
	replace(constants.GitCommitAuthorName, "DD_GIT_COMMIT_AUTHOR_NAME")
	replace(constants.GitCommitAuthorEmail, "DD_GIT_COMMIT_AUTHOR_EMAIL")
	replace(constants.GitCommitAuthorDate, "DD_GIT_COMMIT_AUTHOR_DATE")
	replace(constants.GitCommitCommitterName, "DD_GIT_COMMIT_COMMITTER_NAME")
	replace(constants.GitCommitCommitterEmail, "DD_GIT_COMMIT_COMMITTER_EMAIL")
	replace(constants.GitCommitCommitterDate, "DD_GIT_COMMIT_COMMITTER_DATE")
	replace(constants.GitPrBaseBranch, "DD_GIT_PULL_REQUEST_BASE_BRANCH")
	replace(constants.GitPrBaseCommit, "DD_GIT_PULL_REQUEST_BASE_BRANCH_SHA")
}

// getEnvironmentVariableIfIsNotEmpty returns the environment variable value if it is not empty, otherwise returns the default value.
func getEnvironmentVariableIfIsNotEmpty(key string, defaultValue string) string {
	if value, ok := env.Lookup(key); ok && value != "" {
		return value
	}
	return defaultValue
}

// normalizeRef normalizes a Git reference name by removing common prefixes.
func normalizeRef(name string) string {
	// Define the prefixes to remove
	prefixes := []string{"refs/heads/", "refs/", "origin/", "tags/"}

	// Iterate over prefixes and remove them if present
	for _, prefix := range prefixes {
		if strings.HasPrefix(name, prefix) {
			name = strings.TrimPrefix(name, prefix)
		}
	}
	return name
}

// firstEnv returns the value of the first non-empty environment variable from the provided list.
func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value, ok := env.Lookup(key); ok {
			if value != "" {
				return value
			}
		}
	}
	return ""
}

// extractAppveyor extracts CI information specific to Appveyor.
func extractAppveyor() map[string]string {
	tags := map[string]string{}
	url := fmt.Sprintf("https://ci.appveyor.com/project/%s/builds/%s", env.Get("APPVEYOR_REPO_NAME"), env.Get("APPVEYOR_BUILD_ID"))
	tags[constants.CIProviderName] = "appveyor"
	if env.Get("APPVEYOR_REPO_PROVIDER") == "github" {
		tags[constants.GitRepositoryURL] = fmt.Sprintf("https://github.com/%s.git", env.Get("APPVEYOR_REPO_NAME"))
	} else {
		tags[constants.GitRepositoryURL] = env.Get("APPVEYOR_REPO_NAME")
	}

	tags[constants.GitCommitSHA] = env.Get("APPVEYOR_REPO_COMMIT")
	tags[constants.GitBranch] = firstEnv("APPVEYOR_PULL_REQUEST_HEAD_REPO_BRANCH", "APPVEYOR_REPO_BRANCH")
	tags[constants.GitTag] = env.Get("APPVEYOR_REPO_TAG_NAME")

	tags[constants.CIWorkspacePath] = env.Get("APPVEYOR_BUILD_FOLDER")
	tags[constants.CIPipelineID] = env.Get("APPVEYOR_BUILD_ID")
	tags[constants.CIPipelineName] = env.Get("APPVEYOR_REPO_NAME")
	tags[constants.CIPipelineNumber] = env.Get("APPVEYOR_BUILD_NUMBER")
	tags[constants.CIPipelineURL] = url
	tags[constants.CIJobURL] = url
	tags[constants.GitCommitMessage] = fmt.Sprintf("%s\n%s", env.Get("APPVEYOR_REPO_COMMIT_MESSAGE"), env.Get("APPVEYOR_REPO_COMMIT_MESSAGE_EXTENDED"))
	tags[constants.GitCommitAuthorName] = env.Get("APPVEYOR_REPO_COMMIT_AUTHOR")
	tags[constants.GitCommitAuthorEmail] = env.Get("APPVEYOR_REPO_COMMIT_AUTHOR_EMAIL")

	tags[constants.GitPrBaseBranch] = env.Get("APPVEYOR_REPO_BRANCH")
	tags[constants.GitHeadCommit] = env.Get("APPVEYOR_PULL_REQUEST_HEAD_COMMIT")
	tags[constants.PrNumber] = env.Get("APPVEYOR_PULL_REQUEST_NUMBER")

	return tags
}

// extractAzurePipelines extracts CI information specific to Azure Pipelines.
func extractAzurePipelines() map[string]string {
	tags := map[string]string{}
	baseURL := fmt.Sprintf("%s%s/_build/results?buildId=%s", env.Get("SYSTEM_TEAMFOUNDATIONSERVERURI"), env.Get("SYSTEM_TEAMPROJECTID"), env.Get("BUILD_BUILDID"))
	pipelineURL := baseURL
	jobURL := fmt.Sprintf("%s&view=logs&j=%s&t=%s", baseURL, env.Get("SYSTEM_JOBID"), env.Get("SYSTEM_TASKINSTANCEID"))
	branchOrTag := firstEnv("SYSTEM_PULLREQUEST_SOURCEBRANCH", "BUILD_SOURCEBRANCH", "BUILD_SOURCEBRANCHNAME")
	branch := ""
	tag := ""
	if strings.Contains(branchOrTag, "tags/") {
		tag = branchOrTag
	} else {
		branch = branchOrTag
	}
	tags[constants.CIProviderName] = "azurepipelines"
	tags[constants.CIWorkspacePath] = env.Get("BUILD_SOURCESDIRECTORY")

	tags[constants.CIPipelineID] = env.Get("BUILD_BUILDID")
	tags[constants.CIPipelineName] = env.Get("BUILD_DEFINITIONNAME")
	tags[constants.CIPipelineNumber] = env.Get("BUILD_BUILDID")
	tags[constants.CIPipelineURL] = pipelineURL

	tags[constants.CIStageName] = env.Get("SYSTEM_STAGEDISPLAYNAME")

	tags[constants.CIJobID] = env.Get("SYSTEM_JOBID")
	tags[constants.CIJobName] = env.Get("SYSTEM_JOBDISPLAYNAME")
	tags[constants.CIJobURL] = jobURL

	tags[constants.GitRepositoryURL] = firstEnv("SYSTEM_PULLREQUEST_SOURCEREPOSITORYURI", "BUILD_REPOSITORY_URI")
	tags[constants.GitCommitSHA] = firstEnv("SYSTEM_PULLREQUEST_SOURCECOMMITID", "BUILD_SOURCEVERSION")
	tags[constants.GitBranch] = branch
	tags[constants.GitTag] = tag
	tags[constants.GitCommitMessage] = env.Get("BUILD_SOURCEVERSIONMESSAGE")
	tags[constants.GitCommitAuthorName] = env.Get("BUILD_REQUESTEDFORID")
	tags[constants.GitCommitAuthorEmail] = env.Get("BUILD_REQUESTEDFOREMAIL")

	jsonString, err := getEnvVarsJSON("SYSTEM_TEAMPROJECTID", "BUILD_BUILDID", "SYSTEM_JOBID")
	if err == nil {
		tags[constants.CIEnvVars] = string(jsonString)
	}

	tags[constants.GitPrBaseBranch] = env.Get("SYSTEM_PULLREQUEST_TARGETBRANCH")
	tags[constants.PrNumber] = env.Get("SYSTEM_PULLREQUEST_PULLREQUESTNUMBER")

	return tags
}

// extractBitrise extracts CI information specific to Bitrise.
func extractBitrise() map[string]string {
	tags := map[string]string{}
	tags[constants.CIProviderName] = "bitrise"
	tags[constants.GitRepositoryURL] = env.Get("GIT_REPOSITORY_URL")
	tags[constants.GitCommitSHA] = firstEnv("BITRISE_GIT_COMMIT", "GIT_CLONE_COMMIT_HASH")
	tags[constants.GitBranch] = firstEnv("BITRISEIO_PULL_REQUEST_HEAD_BRANCH", "BITRISE_GIT_BRANCH")
	tags[constants.GitTag] = env.Get("BITRISE_GIT_TAG")
	tags[constants.CIWorkspacePath] = env.Get("BITRISE_SOURCE_DIR")
	tags[constants.CIPipelineID] = env.Get("BITRISE_BUILD_SLUG")
	tags[constants.CIPipelineName] = env.Get("BITRISE_TRIGGERED_WORKFLOW_ID")
	tags[constants.CIPipelineNumber] = env.Get("BITRISE_BUILD_NUMBER")
	tags[constants.CIPipelineURL] = env.Get("BITRISE_BUILD_URL")
	tags[constants.GitCommitMessage] = env.Get("BITRISE_GIT_MESSAGE")

	tags[constants.GitPrBaseBranch] = env.Get("BITRISEIO_GIT_BRANCH_DEST")
	tags[constants.PrNumber] = env.Get("BITRISE_PULL_REQUEST")

	return tags
}

// extractBitbucket extracts CI information specific to Bitbucket.
func extractBitbucket() map[string]string {
	tags := map[string]string{}
	url := fmt.Sprintf("https://bitbucket.org/%s/addon/pipelines/home#!/results/%s", env.Get("BITBUCKET_REPO_FULL_NAME"), env.Get("BITBUCKET_BUILD_NUMBER"))
	tags[constants.CIProviderName] = "bitbucket"
	tags[constants.GitRepositoryURL] = firstEnv("BITBUCKET_GIT_SSH_ORIGIN", "BITBUCKET_GIT_HTTP_ORIGIN")
	tags[constants.GitCommitSHA] = env.Get("BITBUCKET_COMMIT")
	tags[constants.GitBranch] = env.Get("BITBUCKET_BRANCH")
	tags[constants.GitTag] = env.Get("BITBUCKET_TAG")
	tags[constants.CIWorkspacePath] = env.Get("BITBUCKET_CLONE_DIR")
	tags[constants.CIPipelineID] = strings.Trim(env.Get("BITBUCKET_PIPELINE_UUID"), "{}")
	tags[constants.CIPipelineNumber] = env.Get("BITBUCKET_BUILD_NUMBER")
	tags[constants.CIPipelineName] = env.Get("BITBUCKET_REPO_FULL_NAME")
	tags[constants.CIPipelineURL] = url
	tags[constants.CIJobURL] = url

	tags[constants.GitPrBaseBranch] = env.Get("BITBUCKET_PR_DESTINATION_BRANCH")
	tags[constants.PrNumber] = env.Get("BITBUCKET_PR_ID")

	return tags
}

// extractBuddy extracts CI information specific to Buddy.
func extractBuddy() map[string]string {
	tags := map[string]string{}
	tags[constants.CIProviderName] = "buddy"
	tags[constants.CIPipelineID] = fmt.Sprintf("%s/%s", env.Get("BUDDY_PIPELINE_ID"), env.Get("BUDDY_EXECUTION_ID"))
	tags[constants.CIPipelineName] = env.Get("BUDDY_PIPELINE_NAME")
	tags[constants.CIPipelineNumber] = env.Get("BUDDY_EXECUTION_ID")
	tags[constants.CIPipelineURL] = env.Get("BUDDY_EXECUTION_URL")
	tags[constants.GitCommitSHA] = env.Get("BUDDY_EXECUTION_REVISION")
	tags[constants.GitRepositoryURL] = env.Get("BUDDY_SCM_URL")
	tags[constants.GitBranch] = env.Get("BUDDY_EXECUTION_BRANCH")
	tags[constants.GitTag] = env.Get("BUDDY_EXECUTION_TAG")
	tags[constants.GitCommitMessage] = env.Get("BUDDY_EXECUTION_REVISION_MESSAGE")
	tags[constants.GitCommitCommitterName] = env.Get("BUDDY_EXECUTION_REVISION_COMMITTER_NAME")
	tags[constants.GitCommitCommitterEmail] = env.Get("BUDDY_EXECUTION_REVISION_COMMITTER_EMAIL")

	tags[constants.GitPrBaseBranch] = env.Get("BUDDY_RUN_PR_BASE_BRANCH")
	tags[constants.PrNumber] = env.Get("BUDDY_RUN_PR_NO")

	return tags
}

// extractBuildkite extracts CI information specific to Buildkite.
func extractBuildkite() map[string]string {
	tags := map[string]string{}
	tags[constants.GitBranch] = env.Get("BUILDKITE_BRANCH")
	tags[constants.GitCommitSHA] = env.Get("BUILDKITE_COMMIT")
	tags[constants.GitRepositoryURL] = env.Get("BUILDKITE_REPO")
	tags[constants.GitTag] = env.Get("BUILDKITE_TAG")
	tags[constants.CIPipelineID] = env.Get("BUILDKITE_BUILD_ID")
	tags[constants.CIPipelineName] = env.Get("BUILDKITE_PIPELINE_SLUG")
	tags[constants.CIPipelineNumber] = env.Get("BUILDKITE_BUILD_NUMBER")
	tags[constants.CIPipelineURL] = env.Get("BUILDKITE_BUILD_URL")
	tags[constants.CIJobID] = env.Get("BUILDKITE_JOB_ID")
	tags[constants.CIJobURL] = fmt.Sprintf("%s#%s", env.Get("BUILDKITE_BUILD_URL"), env.Get("BUILDKITE_JOB_ID"))
	tags[constants.CIProviderName] = "buildkite"
	tags[constants.CIWorkspacePath] = env.Get("BUILDKITE_BUILD_CHECKOUT_PATH")
	tags[constants.GitCommitMessage] = env.Get("BUILDKITE_MESSAGE")
	tags[constants.GitCommitAuthorName] = env.Get("BUILDKITE_BUILD_AUTHOR")
	tags[constants.GitCommitAuthorEmail] = env.Get("BUILDKITE_BUILD_AUTHOR_EMAIL")
	tags[constants.CINodeName] = env.Get("BUILDKITE_AGENT_ID")

	jsonString, err := getEnvVarsJSON("BUILDKITE_BUILD_ID", "BUILDKITE_JOB_ID")
	if err == nil {
		tags[constants.CIEnvVars] = string(jsonString)
	}

	var extraTags []string
	envVars := os.Environ()
	for _, envVar := range envVars {
		if strings.HasPrefix(envVar, "BUILDKITE_AGENT_META_DATA_") {
			envVarAsTag := envVar
			envVarAsTag = strings.TrimPrefix(envVarAsTag, "BUILDKITE_AGENT_META_DATA_")
			envVarAsTag = strings.ToLower(envVarAsTag)
			envVarAsTag = strings.Replace(envVarAsTag, "=", ":", 1)
			extraTags = append(extraTags, envVarAsTag)
		}
	}

	if len(extraTags) != 0 {
		// HACK: Sorting isn't actually needed, but it simplifies testing if the order is consistent
		sort.Sort(sort.Reverse(sort.StringSlice(extraTags)))
		jsonString, err = json.Marshal(extraTags)
		if err == nil {
			tags[constants.CINodeLabels] = string(jsonString)
		}
	}

	tags[constants.GitPrBaseBranch] = env.Get("BUILDKITE_PULL_REQUEST_BASE_BRANCH")
	tags[constants.PrNumber] = env.Get("BUILDKITE_PULL_REQUEST")

	return tags
}

// extractCircleCI extracts CI information specific to CircleCI.
func extractCircleCI() map[string]string {
	tags := map[string]string{}
	tags[constants.CIProviderName] = "circleci"
	tags[constants.GitRepositoryURL] = env.Get("CIRCLE_REPOSITORY_URL")
	tags[constants.GitCommitSHA] = env.Get("CIRCLE_SHA1")
	tags[constants.GitTag] = env.Get("CIRCLE_TAG")
	tags[constants.GitBranch] = env.Get("CIRCLE_BRANCH")
	tags[constants.CIWorkspacePath] = env.Get("CIRCLE_WORKING_DIRECTORY")
	tags[constants.CIPipelineID] = env.Get("CIRCLE_WORKFLOW_ID")
	tags[constants.CIPipelineName] = env.Get("CIRCLE_PROJECT_REPONAME")
	tags[constants.CIPipelineNumber] = env.Get("CIRCLE_BUILD_NUM")
	tags[constants.CIPipelineURL] = fmt.Sprintf("https://app.circleci.com/pipelines/workflows/%s", env.Get("CIRCLE_WORKFLOW_ID"))
	tags[constants.CIJobName] = env.Get("CIRCLE_JOB")
	tags[constants.CIJobID] = env.Get("CIRCLE_BUILD_NUM")
	tags[constants.CIJobURL] = env.Get("CIRCLE_BUILD_URL")
	tags[constants.PrNumber] = env.Get("CIRCLE_PR_NUMBER")

	jsonString, err := getEnvVarsJSON("CIRCLE_BUILD_NUM", "CIRCLE_WORKFLOW_ID")
	if err == nil {
		tags[constants.CIEnvVars] = string(jsonString)
	}

	return tags
}

// extractGithubActions extracts CI information specific to GitHub Actions.
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

	serverURL := env.Get("GITHUB_SERVER_URL")
	if serverURL == "" {
		serverURL = "https://github.com"
	}
	serverURL = strings.TrimSuffix(serverURL, "/")

	rawRepository := fmt.Sprintf("%s/%s", serverURL, env.Get("GITHUB_REPOSITORY"))
	pipelineID := env.Get("GITHUB_RUN_ID")
	commitSha := env.Get("GITHUB_SHA")

	tags[constants.CIProviderName] = "github"
	tags[constants.GitRepositoryURL] = rawRepository + ".git"
	tags[constants.GitCommitSHA] = commitSha
	tags[constants.GitBranch] = branch
	tags[constants.GitTag] = tag
	tags[constants.CIWorkspacePath] = env.Get("GITHUB_WORKSPACE")
	tags[constants.CIPipelineNumber] = env.Get("GITHUB_RUN_NUMBER")
	tags[constants.CIPipelineName] = env.Get("GITHUB_WORKFLOW")

	// Only set pipeline ID and URL if GITHUB_RUN_ID is present
	if pipelineID != "" {
		tags[constants.CIPipelineID] = pipelineID
		attempts := env.Get("GITHUB_RUN_ATTEMPT")
		if attempts == "" {
			tags[constants.CIPipelineURL] = fmt.Sprintf("%s/actions/runs/%s", rawRepository, pipelineID)
		} else {
			tags[constants.CIPipelineURL] = fmt.Sprintf("%s/actions/runs/%s/attempts/%s", rawRepository, pipelineID, attempts)
		}
	}

	// Resolve job ID and URL
	jobName := env.Get("GITHUB_JOB")
	numericJobID := getGithubActionsJobID()

	tags[constants.CIJobName] = jobName

	if numericJobID != "" && pipelineID != "" {
		tags[constants.CIJobID] = numericJobID
		tags[constants.CIJobURL] = fmt.Sprintf("%s/actions/runs/%s/job/%s", rawRepository, pipelineID, numericJobID)
		log.Debug("civisibility: github actions job url with numeric job id: %s", tags[constants.CIJobURL])
	} else {
		tags[constants.CIJobID] = jobName
		tags[constants.CIJobURL] = fmt.Sprintf("%s/commit/%s/checks", rawRepository, commitSha)
		log.Debug("civisibility: github actions job url fallback: %s", tags[constants.CIJobURL])
	}

	jsonString, err := getEnvVarsJSON("GITHUB_SERVER_URL", "GITHUB_REPOSITORY", "GITHUB_RUN_ID", "GITHUB_RUN_ATTEMPT")
	if err == nil {
		tags[constants.CIEnvVars] = string(jsonString)
	}

	// Extract PR information from the github event json file
	eventFilePath := env.Get("GITHUB_EVENT_PATH")
	if stats, ok := os.Stat(eventFilePath); ok == nil && !stats.IsDir() {
		if eventFile, err := os.Open(eventFilePath); err == nil {
			defer eventFile.Close()

			var eventJSON struct {
				Number      int `json:"number"`
				PullRequest struct {
					Base struct {
						Sha string `json:"sha"`
						Ref string `json:"ref"`
					} `json:"base"`
					Head struct {
						Sha string `json:"sha"`
					} `json:"head"`
				} `json:"pull_request"`
			}

			eventDecoder := json.NewDecoder(eventFile)
			if eventDecoder.Decode(&eventJSON) == nil {
				tags[constants.GitHeadCommit] = eventJSON.PullRequest.Head.Sha
				tags[constants.GitPrBaseHeadCommit] = eventJSON.PullRequest.Base.Sha
				tags[constants.GitPrBaseBranch] = eventJSON.PullRequest.Base.Ref
				tags[constants.PrNumber] = fmt.Sprintf("%d", eventJSON.Number)
			}
		}
	}

	// Fallback if GitPrBaseBranch is not set
	if tmpVal, ok := tags[constants.GitPrBaseBranch]; !ok || tmpVal == "" {
		tags[constants.GitPrBaseBranch] = env.Get("GITHUB_BASE_REF")
	}

	return tags
}

// extractGitlab extracts CI information specific to GitLab.
func extractGitlab() map[string]string {
	tags := map[string]string{}
	url := env.Get("CI_PIPELINE_URL")

	tags[constants.CIProviderName] = "gitlab"
	tags[constants.GitRepositoryURL] = env.Get("CI_REPOSITORY_URL")
	tags[constants.GitCommitSHA] = env.Get("CI_COMMIT_SHA")
	tags[constants.GitBranch] = firstEnv("CI_COMMIT_BRANCH", "CI_COMMIT_REF_NAME")
	tags[constants.GitTag] = env.Get("CI_COMMIT_TAG")
	tags[constants.CIWorkspacePath] = env.Get("CI_PROJECT_DIR")
	tags[constants.CIPipelineID] = env.Get("CI_PIPELINE_ID")
	tags[constants.CIPipelineName] = env.Get("CI_PROJECT_PATH")
	tags[constants.CIPipelineNumber] = env.Get("CI_PIPELINE_IID")
	tags[constants.CIPipelineURL] = url
	tags[constants.CIJobURL] = env.Get("CI_JOB_URL")
	tags[constants.CIJobID] = env.Get("CI_JOB_ID")
	tags[constants.CIJobName] = env.Get("CI_JOB_NAME")
	tags[constants.CIStageName] = env.Get("CI_JOB_STAGE")
	tags[constants.GitCommitMessage] = env.Get("CI_COMMIT_MESSAGE")
	tags[constants.CINodeName] = env.Get("CI_RUNNER_ID")
	tags[constants.CINodeLabels] = env.Get("CI_RUNNER_TAGS")

	author := env.Get("CI_COMMIT_AUTHOR")
	authorArray := strings.FieldsFunc(author, func(s rune) bool {
		return s == '<' || s == '>'
	})
	tags[constants.GitCommitAuthorName] = strings.TrimSpace(authorArray[0])
	tags[constants.GitCommitAuthorEmail] = strings.TrimSpace(authorArray[1])
	tags[constants.GitCommitAuthorDate] = env.Get("CI_COMMIT_TIMESTAMP")

	jsonString, err := getEnvVarsJSON("CI_PROJECT_URL", "CI_PIPELINE_ID", "CI_JOB_ID")
	if err == nil {
		tags[constants.CIEnvVars] = string(jsonString)
	}

	tags[constants.GitHeadCommit] = env.Get("CI_MERGE_REQUEST_SOURCE_BRANCH_SHA")
	tags[constants.GitPrBaseHeadCommit] = env.Get("CI_MERGE_REQUEST_TARGET_BRANCH_SHA")
	tags[constants.GitPrBaseCommit] = env.Get("CI_MERGE_REQUEST_DIFF_BASE_SHA")
	tags[constants.GitPrBaseBranch] = env.Get("CI_MERGE_REQUEST_TARGET_BRANCH_NAME")
	tags[constants.PrNumber] = env.Get("CI_MERGE_REQUEST_IID")

	return tags
}

// extractJenkins extracts CI information specific to Jenkins.
func extractJenkins() map[string]string {
	tags := map[string]string{}
	tags[constants.CIProviderName] = "jenkins"
	tags[constants.GitRepositoryURL] = firstEnv("GIT_URL", "GIT_URL_1")
	tags[constants.GitCommitSHA] = env.Get("GIT_COMMIT")

	branchOrTag := env.Get("GIT_BRANCH")
	empty := []byte("")
	name, hasName := env.Lookup("JOB_NAME")

	if strings.Contains(branchOrTag, "tags/") {
		tags[constants.GitTag] = branchOrTag
	} else {
		tags[constants.GitBranch] = branchOrTag
		// remove branch for job name
		removeBranch := regexp.MustCompile(fmt.Sprintf("/%s", normalizeRef(branchOrTag)))
		name = string(removeBranch.ReplaceAll([]byte(name), empty))
	}

	if hasName {
		removeVars := regexp.MustCompile("/[^/]+=[^/]*")
		name = string(removeVars.ReplaceAll([]byte(name), empty))
	}

	tags[constants.CIWorkspacePath] = env.Get("WORKSPACE")
	tags[constants.CIPipelineID] = env.Get("BUILD_TAG")
	tags[constants.CIPipelineNumber] = env.Get("BUILD_NUMBER")
	tags[constants.CIPipelineName] = name
	tags[constants.CIPipelineURL] = env.Get("BUILD_URL")
	tags[constants.CINodeName] = env.Get("NODE_NAME")
	tags[constants.PrNumber] = env.Get("CHANGE_ID")
	tags[constants.GitPrBaseBranch] = env.Get("CHANGE_TARGET")

	jsonString, err := getEnvVarsJSON("DD_CUSTOM_TRACE_ID")
	if err == nil {
		tags[constants.CIEnvVars] = string(jsonString)
	}

	nodeLabels := env.Get("NODE_LABELS")
	if len(nodeLabels) > 0 {
		labelsArray := strings.Split(nodeLabels, " ")
		jsonString, err := json.Marshal(labelsArray)
		if err == nil {
			tags[constants.CINodeLabels] = string(jsonString)
		}
	}

	return tags
}

// extractTeamcity extracts CI information specific to TeamCity.
func extractTeamcity() map[string]string {
	tags := map[string]string{}
	tags[constants.CIProviderName] = "teamcity"
	tags[constants.CIJobURL] = env.Get("BUILD_URL")
	tags[constants.CIJobName] = env.Get("TEAMCITY_BUILDCONF_NAME")

	tags[constants.PrNumber] = env.Get("TEAMCITY_PULLREQUEST_NUMBER")
	tags[constants.GitPrBaseBranch] = env.Get("TEAMCITY_PULLREQUEST_TARGET_BRANCH")
	return tags
}

// extractCodefresh extracts CI information specific to Codefresh.
func extractCodefresh() map[string]string {
	tags := map[string]string{}
	tags[constants.CIProviderName] = "codefresh"
	tags[constants.CIPipelineID] = env.Get("CF_BUILD_ID")
	tags[constants.CIPipelineName] = env.Get("CF_PIPELINE_NAME")
	tags[constants.CIPipelineURL] = env.Get("CF_BUILD_URL")
	tags[constants.CIJobName] = env.Get("CF_STEP_NAME")

	jsonString, err := getEnvVarsJSON("CF_BUILD_ID")
	if err == nil {
		tags[constants.CIEnvVars] = string(jsonString)
	}

	cfBranch := env.Get("CF_BRANCH")
	isTag := strings.Contains(cfBranch, "tags/")
	var refKey string
	if isTag {
		refKey = constants.GitTag
	} else {
		refKey = constants.GitBranch
	}
	tags[refKey] = normalizeRef(cfBranch)

	tags[constants.GitPrBaseBranch] = env.Get("CF_PULL_REQUEST_TARGET")
	tags[constants.PrNumber] = env.Get("CF_PULL_REQUEST_NUMBER")

	return tags
}

// extractTravis extracts CI information specific to Travis CI.
func extractTravis() map[string]string {
	tags := map[string]string{}
	prSlug := env.Get("TRAVIS_PULL_REQUEST_SLUG")
	repoSlug := prSlug
	if strings.TrimSpace(repoSlug) == "" {
		repoSlug = env.Get("TRAVIS_REPO_SLUG")
	}
	tags[constants.CIProviderName] = "travisci"
	tags[constants.GitRepositoryURL] = fmt.Sprintf("https://github.com/%s.git", repoSlug)
	tags[constants.GitCommitSHA] = env.Get("TRAVIS_COMMIT")
	tags[constants.GitTag] = env.Get("TRAVIS_TAG")
	tags[constants.GitBranch] = firstEnv("TRAVIS_PULL_REQUEST_BRANCH", "TRAVIS_BRANCH")
	tags[constants.CIWorkspacePath] = env.Get("TRAVIS_BUILD_DIR")
	tags[constants.CIPipelineID] = env.Get("TRAVIS_BUILD_ID")
	tags[constants.CIPipelineNumber] = env.Get("TRAVIS_BUILD_NUMBER")
	tags[constants.CIPipelineName] = repoSlug
	tags[constants.CIPipelineURL] = env.Get("TRAVIS_BUILD_WEB_URL")
	tags[constants.CIJobURL] = env.Get("TRAVIS_JOB_WEB_URL")
	tags[constants.GitCommitMessage] = env.Get("TRAVIS_COMMIT_MESSAGE")

	tags[constants.GitPrBaseBranch] = env.Get("TRAVIS_BRANCH")
	tags[constants.GitHeadCommit] = env.Get("TRAVIS_PULL_REQUEST_SHA")
	tags[constants.PrNumber] = env.Get("TRAVIS_PULL_REQUEST")

	return tags
}

// extractAwsCodePipeline extracts CI information specific to AWS CodePipeline.
func extractAwsCodePipeline() map[string]string {
	tags := map[string]string{}

	if !strings.HasPrefix(env.Get("CODEBUILD_INITIATOR"), "codepipeline") {
		// CODEBUILD_INITIATOR is defined but this is not a codepipeline build
		return tags
	}

	tags[constants.CIProviderName] = "awscodepipeline"
	tags[constants.CIPipelineID] = env.Get("DD_PIPELINE_EXECUTION_ID")
	tags[constants.CIJobID] = env.Get("DD_ACTION_EXECUTION_ID")

	jsonString, err := getEnvVarsJSON("CODEBUILD_BUILD_ARN", "DD_ACTION_EXECUTION_ID", "DD_PIPELINE_EXECUTION_ID")
	if err == nil {
		tags[constants.CIEnvVars] = string(jsonString)
	}

	return tags
}

// extractDrone extracts CI information specific to Drone CI.
func extractDrone() map[string]string {
	tags := map[string]string{}
	tags[constants.CIProviderName] = "drone"
	tags[constants.GitBranch] = env.Get("DRONE_BRANCH")
	tags[constants.GitCommitSHA] = env.Get("DRONE_COMMIT_SHA")
	tags[constants.GitRepositoryURL] = env.Get("DRONE_GIT_HTTP_URL")
	tags[constants.GitTag] = env.Get("DRONE_TAG")
	tags[constants.CIPipelineNumber] = env.Get("DRONE_BUILD_NUMBER")
	tags[constants.CIPipelineURL] = env.Get("DRONE_BUILD_LINK")
	tags[constants.GitCommitMessage] = env.Get("DRONE_COMMIT_MESSAGE")
	tags[constants.GitCommitAuthorName] = env.Get("DRONE_COMMIT_AUTHOR_NAME")
	tags[constants.GitCommitAuthorEmail] = env.Get("DRONE_COMMIT_AUTHOR_EMAIL")
	tags[constants.CIWorkspacePath] = env.Get("DRONE_WORKSPACE")
	tags[constants.CIJobName] = env.Get("DRONE_STEP_NAME")
	tags[constants.CIStageName] = env.Get("DRONE_STAGE_NAME")
	tags[constants.PrNumber] = env.Get("DRONE_PULL_REQUEST")
	tags[constants.GitPrBaseBranch] = env.Get("DRONE_TARGET_BRANCH")

	return tags
}

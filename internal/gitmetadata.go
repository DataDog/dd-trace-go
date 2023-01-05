// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package internal

import (
	"os"
	"reflect"
	"runtime/debug"
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

const (
	envEnabledFlag   = "DD_TRACE_GIT_METADATA_ENABLED"
	envRepositoryURL = "DD_GIT_REPOSITORY_URL"
	envCommitSha     = "DD_GIT_COMMIT_SHA"
	envMainPackage   = "DD_MAIN_PACKAGE"
	envGlobalTags    = "DD_TAGS"

	tagRepositoryURL = "git.repository_url"
	tagCommitSha     = "git.commit.sha"

	//traceTagRepositoryURL = "_dd.git.repository_url"
	//traceTagCommitSha     = "_dd.git.commit.sha"
	traceTagRepositoryURL = "git.repository_url"
	traceTagCommitSha     = "git.commit.sha"
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

func checkField(i interface{}, filedName string) bool {
	metaValue := reflect.ValueOf(i).Elem()
	field := metaValue.FieldByName(filedName)
	return field != reflect.Value{}
}

// getTagsFrom binalry extracts git metadata from binary metadata:
func getTagsFromBinary() map[string]string {
	res := make(map[string]string)
	info, ok := debug.ReadBuildInfo()
	if !ok {
		log.Warn("ReadBuildInfo failed, skip source code metadata extracting")
		return res
	}

	if !checkField(info, "Settings") {
		log.Warn("BuildInfo has no Settings filed (go < 1.18), skip source code metadata extracting")
		return res
	}

	repositoryURL := info.Path
	vcs := ""
	commitSha := ""
	for _, s := range info.Settings {
		if s.Key == "vcs" {
			vcs = s.Value
		} else if s.Key == "vcs.revision" {
			commitSha = s.Value
		}
	}
	if vcs != "git" {
		log.Warn("Unknown VCS: '%s', skip source code metadata extracting", vcs)
		return res
	}
	res[tagCommitSha] = commitSha
	res[tagRepositoryURL] = repositoryURL
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

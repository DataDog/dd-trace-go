// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package net

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/bazel"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

const (
	testManagementTestsRequestType string = "ci_app_libraries_tests_request"
	testManagementTestsURLPath     string = "api/v2/test/libraries/test-management/tests"
)

type (
	testManagementTestsRequest struct {
		Data testManagementTestsRequestHeader `json:"data"`
	}

	testManagementTestsRequestHeader struct {
		ID         string                         `json:"id"`
		Type       string                         `json:"type"`
		Attributes testManagementTestsRequestData `json:"attributes"`
	}

	testManagementTestsRequestData struct {
		RepositoryURL string `json:"repository_url"`
		CommitSha     string `json:"sha"`
		Module        string `json:"module,omitempty"`
		CommitMessage string `json:"commit_message"`
		Branch        string `json:"branch"`
	}

	testManagementTestsResponse struct {
		Data struct {
			ID         string                                 `json:"id"`
			Type       string                                 `json:"type"`
			Attributes TestManagementTestsResponseDataModules `json:"attributes"`
		} `json:"data"`
	}

	TestManagementTestsResponseDataModules struct {
		Modules map[string]TestManagementTestsResponseDataSuites `json:"modules"`
	}

	TestManagementTestsResponseDataSuites struct {
		Suites map[string]TestManagementTestsResponseDataTests `json:"suites"`
	}

	TestManagementTestsResponseDataTests struct {
		Tests map[string]TestManagementTestsResponseDataTestProperties `json:"tests"`
	}

	TestManagementTestsResponseDataTestProperties struct {
		Properties TestManagementTestsResponseDataTestPropertiesAttributes `json:"properties"`
	}

	TestManagementTestsResponseDataTestPropertiesAttributes struct {
		Quarantined  bool `json:"quarantined"`
		Disabled     bool `json:"disabled"`
		AttemptToFix bool `json:"attempt_to_fix"`
	}
)

// GetTestManagementTests loads cached test-management data in manifest mode and otherwise queries the live endpoint for the selected commit.
func (c *client) GetTestManagementTests() (*TestManagementTestsResponseDataModules, error) {
	if bazel.IsManifestModeEnabled() {
		if cachedResponse, ok := loadTestManagementFromManifestCache(); ok {
			return cachedResponse, nil
		}
		// Compatible with Bazel offline mode: missing or invalid cache means empty test management response.
		log.Debug("civisibility.test_management: returning empty test management response because manifest cache is unavailable or invalid")
		return &TestManagementTestsResponseDataModules{
			Modules: map[string]TestManagementTestsResponseDataSuites{},
		}, nil
	}

	if c.repositoryURL == "" {
		return nil, fmt.Errorf("civisibility.GetTestManagementTests: repository URL is required")
	}

	// we use the head commit SHA if it is set, otherwise we use the commit SHA
	commitSha := c.commitSha
	if c.headCommitSha != "" {
		commitSha = c.headCommitSha
	}

	// we use the head commit message if it is set, otherwise we use the commit message
	commitMessage := c.commitMessage
	if c.headCommitMessage != "" {
		commitMessage = c.headCommitMessage
	}

	body := testManagementTestsRequest{
		Data: testManagementTestsRequestHeader{
			ID:   c.id,
			Type: testManagementTestsRequestType,
			Attributes: testManagementTestsRequestData{
				RepositoryURL: c.repositoryURL,
				CommitSha:     commitSha,
				CommitMessage: commitMessage,
				Branch:        c.branchName,
			},
		},
	}

	request := c.getPostRequestConfig(testManagementTestsURLPath, body)
	if request.Compressed {
		telemetry.TestManagementTestsRequest(telemetry.CompressedRequestCompressedType)
	} else {
		telemetry.TestManagementTestsRequest(telemetry.UncompressedRequestCompressedType)
	}

	startTime := time.Now()
	response, err := c.handler.SendRequest(*request)
	telemetry.TestManagementTestsRequestMs(float64(time.Since(startTime).Milliseconds()))

	if err != nil {
		telemetry.TestManagementTestsRequestErrors(telemetry.NetworkErrorType)
		return nil, fmt.Errorf("sending known tests request: %s", err)
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		telemetry.TestManagementTestsRequestErrors(telemetry.GetErrorTypeFromStatusCode(response.StatusCode))
	}
	if response.Compressed {
		telemetry.TestManagementTestsResponseBytes(telemetry.CompressedResponseCompressedType, float64(len(response.Body)))
	} else {
		telemetry.TestManagementTestsResponseBytes(telemetry.UncompressedResponseCompressedType, float64(len(response.Body)))
	}

	var responseObject testManagementTestsResponse
	err = response.Unmarshal(&responseObject)
	if err != nil {
		return nil, fmt.Errorf("unmarshalling test management tests response: %s", err)
	}

	testCount := 0
	if responseObject.Data.Attributes.Modules != nil {
		for _, module := range responseObject.Data.Attributes.Modules {
			if module.Suites == nil {
				continue
			}
			for _, suite := range module.Suites {
				if suite.Tests == nil {
					continue
				}
				testCount += len(suite.Tests)
			}
		}
	}
	telemetry.TestManagementTestsResponseTests(float64(testCount))
	return &responseObject.Data.Attributes, nil
}

// loadTestManagementFromManifestCache reads and validates the Bazel manifest cache file for test-management data.
// It returns the cached response only when the cache path resolves, the file can be read, and the JSON is valid.
func loadTestManagementFromManifestCache() (*TestManagementTestsResponseDataModules, bool) {
	cacheFile, ok := bazel.CacheHTTPFile("test_management.json")
	if !ok {
		log.Debug("civisibility.test_management: manifest mode enabled but test management cache path could not be resolved")
		return nil, false
	}

	cacheFileForLog := bazel.TestOptimizationPathForLog(cacheFile)
	log.Debug("civisibility.test_management: reading %s", cacheFileForLog)

	raw, err := os.ReadFile(cacheFile)
	if err != nil {
		log.Debug("civisibility.test_management: cannot read test management file %s: %s", cacheFileForLog, err.Error())
		return nil, false
	}

	log.Debug("civisibility.test_management: read %s (%d bytes)", cacheFileForLog, len(raw))

	var cachedResponse testManagementTestsResponse
	if err := json.Unmarshal(raw, &cachedResponse); err != nil {
		log.Debug("civisibility.test_management: invalid test management file %s: %s", cacheFileForLog, err.Error())
		return nil, false
	}

	moduleCount := 0
	suiteCount := 0
	testCount := 0
	for _, module := range cachedResponse.Data.Attributes.Modules {
		moduleCount++
		if module.Suites == nil {
			continue
		}
		for _, suite := range module.Suites {
			suiteCount++
			if suite.Tests == nil {
				continue
			}
			testCount += len(suite.Tests)
		}
	}
	log.Debug("civisibility.test_management: loaded test management tests from %s [modules:%d suites:%d tests:%d]", cacheFileForLog, moduleCount, suiteCount, testCount)
	return &cachedResponse.Data.Attributes, true
}

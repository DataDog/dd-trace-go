// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package net

import (
	"fmt"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/telemetry"
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

func (c *client) GetTestManagementTests() (*TestManagementTestsResponseDataModules, error) {
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

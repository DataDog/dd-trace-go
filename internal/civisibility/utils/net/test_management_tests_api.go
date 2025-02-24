// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package net

import (
	"fmt"
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
		Module        string `json:"module"`
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

	body := testManagementTestsRequest{
		Data: testManagementTestsRequestHeader{
			ID:   c.id,
			Type: testManagementTestsRequestType,
			Attributes: testManagementTestsRequestData{
				RepositoryURL: c.repositoryURL,
			},
		},
	}

	request := c.getPostRequestConfig(testManagementTestsURLPath, body)
	response, err := c.handler.SendRequest(*request)
	if err != nil {
		return nil, fmt.Errorf("sending known tests request: %s", err.Error())
	}

	var responseObject testManagementTestsResponse
	err = response.Unmarshal(&responseObject)
	if err != nil {
		return nil, fmt.Errorf("unmarshalling test management tests response: %s", err.Error())
	}

	return &responseObject.Data.Attributes, nil
}

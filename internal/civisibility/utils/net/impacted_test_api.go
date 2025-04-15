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
	impactedTestsRequestType string = "ci_app_tests_diffs_request"
	impactedTestsURLPath     string = "api/v2/ci/tests/diffs"
)

type (
	impactedTestsRequest struct {
		Data impactedTestsRequestHeader `json:"data"`
	}

	impactedTestsRequestHeader struct {
		ID         string                            `json:"id"`
		Type       string                            `json:"type"`
		Attributes ImpactedTestsDetectionRequestData `json:"attributes"`
	}

	ImpactedTestsDetectionRequestData struct {
		Service       string `json:"service"`
		Environment   string `json:"env"`
		RepositoryURL string `json:"repository_url"`
		Branch        string `json:"branch"`
		Sha           string `json:"sha"`
	}

	impactedTestsResponse struct {
		Data struct {
			ID         string                         `json:"id"`
			Type       string                         `json:"type"`
			Attributes ImpactedTestsDetectionResponse `json:"attributes"`
		} `json:"data"`
	}

	ImpactedTestsDetectionResponse struct {
		BaseSha string   `json:"base_sha"`
		Files   []string `json:"files"`
	}
)

func (c *client) GetImpactedTests() (*ImpactedTestsDetectionResponse, error) {
	if c.repositoryURL == "" || c.commitSha == "" {
		return nil, fmt.Errorf("civisibility.GetImpactedTests: repository URL and commit SHA are required")
	}

	body := impactedTestsRequest{
		Data: impactedTestsRequestHeader{
			ID:   c.id,
			Type: impactedTestsRequestType,
			Attributes: ImpactedTestsDetectionRequestData{
				Service:       c.serviceName,
				Environment:   c.environment,
				RepositoryURL: c.repositoryURL,
				Branch:        c.branchName,
				Sha:           c.commitSha,
			},
		},
	}

	request := c.getPostRequestConfig(impactedTestsURLPath, body)
	if request.Compressed {
		telemetry.ImpactedTestsRequest(telemetry.CompressedRequestCompressedType)
	} else {
		telemetry.ImpactedTestsRequest(telemetry.UncompressedRequestCompressedType)
	}

	startTime := time.Now()
	response, err := c.handler.SendRequest(*request)
	telemetry.ImpactedTestsRequestMs(float64(time.Since(startTime).Milliseconds()))

	if err != nil {
		telemetry.ImpactedTestsRequestErrors(telemetry.NetworkErrorType)
		return nil, fmt.Errorf("sending impacted tests request: %s", err.Error())
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		telemetry.ImpactedTestsRequestErrors(telemetry.GetErrorTypeFromStatusCode(response.StatusCode))
	}
	if response.Compressed {
		telemetry.ImpactedTestsResponseBytes(telemetry.CompressedResponseCompressedType, float64(len(response.Body)))
	} else {
		telemetry.ImpactedTestsResponseBytes(telemetry.UncompressedResponseCompressedType, float64(len(response.Body)))
	}

	var responseObject impactedTestsResponse
	err = response.Unmarshal(&responseObject)
	if err != nil {
		return nil, fmt.Errorf("unmarshalling impacted tests response: %s", err.Error())
	}

	filesCount := 0
	if responseObject.Data.Attributes.Files != nil {
		filesCount = len(responseObject.Data.Attributes.Files)
	}
	telemetry.ImpactedTestsResponseFiles(float64(filesCount))
	return &responseObject.Data.Attributes, nil
}

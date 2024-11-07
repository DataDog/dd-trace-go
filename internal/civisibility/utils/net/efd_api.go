// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package net

import (
	"fmt"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils/telemetry"
)

const (
	efdRequestType string = "ci_app_libraries_tests_request"
	efdURLPath     string = "api/v2/ci/libraries/tests"
)

type (
	efdRequest struct {
		Data efdRequestHeader `json:"data"`
	}

	efdRequestHeader struct {
		ID         string         `json:"id"`
		Type       string         `json:"type"`
		Attributes EfdRequestData `json:"attributes"`
	}

	EfdRequestData struct {
		Service        string             `json:"service"`
		Env            string             `json:"env"`
		RepositoryURL  string             `json:"repository_url"`
		Configurations testConfigurations `json:"configurations"`
	}

	efdResponse struct {
		Data struct {
			ID         string          `json:"id"`
			Type       string          `json:"type"`
			Attributes EfdResponseData `json:"attributes"`
		} `json:"data"`
	}

	EfdResponseData struct {
		Tests EfdResponseDataModules `json:"tests"`
	}

	EfdResponseDataModules map[string]EfdResponseDataSuites
	EfdResponseDataSuites  map[string][]string
)

func (c *client) GetEarlyFlakeDetectionData() (*EfdResponseData, error) {
	body := efdRequest{
		Data: efdRequestHeader{
			ID:   c.id,
			Type: efdRequestType,
			Attributes: EfdRequestData{
				Service:        c.serviceName,
				Env:            c.environment,
				RepositoryURL:  c.repositoryURL,
				Configurations: c.testConfigurations,
			},
		},
	}

	request := c.getPostRequestConfig(efdURLPath, body)
	if request.Compressed {
		telemetry.EarlyFlakeDetectionRequest(telemetry.CompressedRequestCompressedType)
	} else {
		telemetry.EarlyFlakeDetectionRequest(telemetry.UncompressedRequestCompressedType)
	}

	startTime := time.Now()
	response, err := c.handler.SendRequest(*request)
	telemetry.EarlyFlakeDetectionRequestMs(float64(time.Since(startTime).Milliseconds()))

	if err != nil {
		telemetry.EarlyFlakeDetectionRequestErrors(telemetry.NetworkErrorType)
		return nil, fmt.Errorf("sending early flake detection request: %s", err.Error())
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		telemetry.EarlyFlakeDetectionRequestErrors(telemetry.GetErrorTypeFromStatusCode(response.StatusCode))
	}
	if response.Compressed {
		telemetry.EarlyFlakeDetectionResponseBytes(telemetry.CompressedResponseCompressedType, float64(len(response.Body)))
	} else {
		telemetry.EarlyFlakeDetectionResponseBytes(telemetry.UncompressedResponseCompressedType, float64(len(response.Body)))
	}

	var responseObject efdResponse
	err = response.Unmarshal(&responseObject)
	if err != nil {
		return nil, fmt.Errorf("unmarshalling early flake detection data response: %s", err.Error())
	}

	testCount := 0
	if responseObject.Data.Attributes.Tests != nil {
		for _, suites := range responseObject.Data.Attributes.Tests {
			if suites == nil {
				continue
			}
			for _, tests := range suites {
				testCount += len(tests)
			}
		}
	}
	telemetry.EarlyFlakeDetectionResponseTests(float64(testCount))
	return &responseObject.Data.Attributes, nil
}

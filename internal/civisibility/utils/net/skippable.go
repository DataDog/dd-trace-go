// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package net

import (
	"fmt"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/bazel"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/telemetry"
)

const (
	skippableRequestType string = "test_params"
	skippableURLPath     string = "api/v2/ci/tests/skippable"
)

type (
	skippableRequest struct {
		Data skippableRequestHeader `json:"data"`
	}

	skippableRequestHeader struct {
		Type       string               `json:"type"`
		Attributes skippableRequestData `json:"attributes"`
	}

	skippableRequestData struct {
		TestLevel      string             `json:"test_level"`
		Configurations testConfigurations `json:"configurations"`
		Service        string             `json:"service"`
		Env            string             `json:"env"`
		RepositoryURL  string             `json:"repository_url"`
		Sha            string             `json:"sha"`
	}

	skippableResponse struct {
		Meta skippableResponseMeta   `json:"meta"`
		Data []skippableResponseData `json:"data"`
	}

	skippableResponseMeta struct {
		CorrelationID string `json:"correlation_id"`
	}

	skippableResponseData struct {
		ID         string                          `json:"id"`
		Type       string                          `json:"type"`
		Attributes SkippableResponseDataAttributes `json:"attributes"`
	}

	SkippableResponseDataAttributes struct {
		Suite          string             `json:"suite"`
		Name           string             `json:"name"`
		Parameters     string             `json:"parameters"`
		Configurations testConfigurations `json:"configurations"`
	}

	// cachedSkippableTests stores the filtered skippable payload plus original response count.
	cachedSkippableTests struct {
		CorrelationID      string                                                  `json:"correlation_id"`
		Skippables         map[string]map[string][]SkippableResponseDataAttributes `json:"skippables"`
		ResponseTestsCount int                                                     `json:"response_tests_count"`
	}
)

func (c *client) GetSkippableTests() (correlationID string, skippables map[string]map[string][]SkippableResponseDataAttributes, err error) {
	if bazel.IsManifestModeEnabled() {
		return "", map[string]map[string][]SkippableResponseDataAttributes{}, nil
	}

	if c.repositoryURL == "" || c.commitSha == "" {
		err = fmt.Errorf("civisibility.GetSkippableTests: repository URL and commit SHA are required")
		return
	}

	body := skippableRequest{
		Data: skippableRequestHeader{
			Type: skippableRequestType,
			Attributes: skippableRequestData{
				TestLevel:      "test",
				Configurations: c.testConfigurations,
				Service:        c.serviceName,
				Env:            c.environment,
				RepositoryURL:  c.repositoryURL,
				Sha:            c.commitSha,
			},
		},
	}

	result, err := readThroughShortLivedCache(
		c,
		readCacheEndpointSkippableTests,
		body,
		func() (readCacheLiveResult[cachedSkippableTests], error) {
			request := c.getPostRequestConfig(skippableURLPath, body)
			if request.Compressed {
				telemetry.ITRSkippableTestsRequest(telemetry.CompressedRequestCompressedType)
			} else {
				telemetry.ITRSkippableTestsRequest(telemetry.UncompressedRequestCompressedType)
			}

			startTime := time.Now()
			response, err := c.handler.SendRequest(*request)
			telemetry.ITRSkippableTestsRequestMs(float64(time.Since(startTime).Milliseconds()))

			if err != nil {
				telemetry.ITRSkippableTestsRequestErrors(telemetry.NetworkErrorType)
				return readCacheLiveResult[cachedSkippableTests]{}, fmt.Errorf("sending skippable tests request: %s", err)
			}

			if response.StatusCode < 200 || response.StatusCode >= 300 {
				telemetry.ITRSkippableTestsRequestErrors(telemetry.GetErrorTypeFromStatusCode(response.StatusCode))
			}

			if response.Compressed {
				telemetry.ITRSkippableTestsResponseBytes(telemetry.CompressedResponseCompressedType, float64(len(response.Body)))
			} else {
				telemetry.ITRSkippableTestsResponseBytes(telemetry.UncompressedResponseCompressedType, float64(len(response.Body)))
			}

			var responseObject skippableResponse
			err = response.Unmarshal(&responseObject)
			if err != nil {
				return readCacheLiveResult[cachedSkippableTests]{}, fmt.Errorf("unmarshalling skippable tests response: %s", err)
			}

			responseTestsCount := len(responseObject.Data)
			telemetry.ITRSkippableTestsResponseTests(float64(responseTestsCount))
			skippableTestsMap := map[string]map[string][]SkippableResponseDataAttributes{}
			for _, data := range responseObject.Data {

				// Filter out the tests that do not match the test configurations
				if data.Attributes.Configurations.OsPlatform != "" && c.testConfigurations.OsPlatform != "" &&
					data.Attributes.Configurations.OsPlatform != c.testConfigurations.OsPlatform {
					continue
				}
				if data.Attributes.Configurations.OsArchitecture != "" && c.testConfigurations.OsArchitecture != "" &&
					data.Attributes.Configurations.OsArchitecture != c.testConfigurations.OsArchitecture {
					continue
				}
				if data.Attributes.Configurations.OsVersion != "" && c.testConfigurations.OsVersion != "" &&
					data.Attributes.Configurations.OsVersion != c.testConfigurations.OsVersion {
					continue
				}
				if data.Attributes.Configurations.RuntimeName != "" && c.testConfigurations.RuntimeName != "" &&
					data.Attributes.Configurations.RuntimeName != c.testConfigurations.RuntimeName {
					continue
				}
				if data.Attributes.Configurations.RuntimeArchitecture != "" && c.testConfigurations.RuntimeArchitecture != "" &&
					data.Attributes.Configurations.RuntimeArchitecture != c.testConfigurations.RuntimeArchitecture {
					continue
				}
				if data.Attributes.Configurations.RuntimeVersion != "" && c.testConfigurations.RuntimeVersion != "" &&
					data.Attributes.Configurations.RuntimeVersion != c.testConfigurations.RuntimeVersion {
					continue
				}

				var ok bool
				var testsMap map[string][]SkippableResponseDataAttributes
				if testsMap, ok = skippableTestsMap[data.Attributes.Suite]; !ok {
					testsMap = map[string][]SkippableResponseDataAttributes{}
					skippableTestsMap[data.Attributes.Suite] = testsMap
				}

				if test, ok := testsMap[data.Attributes.Name]; ok {
					testsMap[data.Attributes.Name] = append(test, data.Attributes)
				} else {
					testsMap[data.Attributes.Name] = []SkippableResponseDataAttributes{data.Attributes}
				}
			}

			value := cachedSkippableTests{
				CorrelationID:      responseObject.Meta.CorrelationID,
				Skippables:         skippableTestsMap,
				ResponseTestsCount: responseTestsCount,
			}
			return readCacheLiveResult[cachedSkippableTests]{
				Value:     value,
				Cacheable: response.StatusCode >= 200 && response.StatusCode < 300,
			}, nil
		},
		func(cached cachedSkippableTests) {
			telemetry.ITRSkippableTestsResponseTests(float64(cached.ResponseTestsCount))
		},
	)
	if err != nil {
		return "", nil, err
	}
	return result.CorrelationID, result.Skippables, nil
}

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package net

import (
	"fmt"
)

const (
	skippableRequestType string = "test_params"
	skippableURLPath     string = "/api/v2/ci/tests/skippable"
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
		CorrelationId string `json:"correlation_id"`
	}

	skippableResponseData struct {
		Id         string                          `json:"id"`
		Type       string                          `json:"type"`
		Attributes SkippableResponseDataAttributes `json:"attributes"`
	}

	SkippableResponseDataAttributes struct {
		Suite          string             `json:"suite"`
		Name           string             `json:"name"`
		Parameters     string             `json:"parameters"`
		Configurations testConfigurations `json:"configurations"`
	}
)

func (c *client) GetSkippableTests() (correlationId string, skippables *[]SkippableResponseDataAttributes, err error) {

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

	response, err := c.handler.SendRequest(*c.getPostRequestConfig(skippableURLPath, body))
	if err != nil {
		return "", nil, fmt.Errorf("sending skippable tests request: %s", err.Error())
	}

	var responseObject skippableResponse
	err = response.Unmarshal(&responseObject)
	if err != nil {
		return "", nil, fmt.Errorf("unmarshalling skippable tests response: %s", err.Error())
	}

	var skippableTests []SkippableResponseDataAttributes
	for _, data := range responseObject.Data {
		skippableTests = append(skippableTests, data.Attributes)
	}

	return responseObject.Meta.CorrelationId, &skippableTests, nil
}

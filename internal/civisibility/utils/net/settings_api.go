// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package net

import (
	"fmt"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils/telemetry"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

const (
	settingsRequestType string = "ci_app_test_service_libraries_settings"
	settingsURLPath     string = "api/v2/libraries/tests/services/setting"
)

type (
	settingsRequest struct {
		Data settingsRequestHeader `json:"data"`
	}

	settingsRequestHeader struct {
		ID         string              `json:"id"`
		Type       string              `json:"type"`
		Attributes SettingsRequestData `json:"attributes"`
	}

	SettingsRequestData struct {
		Service        string             `json:"service,omitempty"`
		Env            string             `json:"env,omitempty"`
		RepositoryURL  string             `json:"repository_url,omitempty"`
		Branch         string             `json:"branch,omitempty"`
		Sha            string             `json:"sha,omitempty"`
		Configurations testConfigurations `json:"configurations,omitempty"`
	}

	settingsResponse struct {
		Data struct {
			ID         string               `json:"id"`
			Type       string               `json:"type"`
			Attributes SettingsResponseData `json:"attributes"`
		} `json:"data,omitempty"`
	}

	SettingsResponseData struct {
		CodeCoverage        bool `json:"code_coverage"`
		EarlyFlakeDetection struct {
			Enabled         bool `json:"enabled"`
			SlowTestRetries struct {
				TenS    int `json:"10s"`
				ThirtyS int `json:"30s"`
				FiveM   int `json:"5m"`
				FiveS   int `json:"5s"`
			} `json:"slow_test_retries"`
			FaultySessionThreshold int `json:"faulty_session_threshold"`
		} `json:"early_flake_detection"`
		FlakyTestRetriesEnabled bool `json:"flaky_test_retries_enabled"`
		ItrEnabled              bool `json:"itr_enabled"`
		RequireGit              bool `json:"require_git"`
		TestsSkipping           bool `json:"tests_skipping"`
	}
)

func (c *client) GetSettings() (*SettingsResponseData, error) {
	body := settingsRequest{
		Data: settingsRequestHeader{
			ID:   c.id,
			Type: settingsRequestType,
			Attributes: SettingsRequestData{
				Service:        c.serviceName,
				Env:            c.environment,
				RepositoryURL:  c.repositoryURL,
				Branch:         c.branchName,
				Sha:            c.commitSha,
				Configurations: c.testConfigurations,
			},
		},
	}

	request := c.getPostRequestConfig(settingsURLPath, body)
	if request.Compressed {
		telemetry.GitRequestsSettings(telemetry.CompressedRequestCompressedType)
	} else {
		telemetry.GitRequestsSettings(telemetry.UncompressedRequestCompressedType)
	}

	startTime := time.Now()
	response, err := c.handler.SendRequest(*request)
	telemetry.GitRequestsSettingsMs(float64(time.Since(startTime).Milliseconds()))
	if err != nil {
		telemetry.GitRequestsSettingsErrors(telemetry.NetworkErrorType)
		return nil, fmt.Errorf("sending get settings request: %s", err.Error())
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		telemetry.GitRequestsSettingsErrors(telemetry.GetErrorTypeFromStatusCode(response.StatusCode))
	}

	var responseObject settingsResponse
	err = response.Unmarshal(&responseObject)
	if err != nil {
		return nil, fmt.Errorf("unmarshalling settings response: %s", err.Error())
	}

	if log.DebugEnabled() {
		log.Debug("civisibility.settings: %s", string(response.Body))
	}

	var settingsResponseType telemetry.SettingsResponseType
	if responseObject.Data.Attributes.CodeCoverage {
		settingsResponseType = append(settingsResponseType, telemetry.CoverageEnabledSettingsResponseType...)
	}
	if responseObject.Data.Attributes.TestsSkipping {
		settingsResponseType = append(settingsResponseType, telemetry.ItrSkipEnabledSettingsResponseType...)
	}
	if responseObject.Data.Attributes.EarlyFlakeDetection.Enabled {
		settingsResponseType = append(settingsResponseType, telemetry.EfdEnabledSettingsResponseType...)
	}
	telemetry.GitRequestsSettingsResponse(settingsResponseType)
	return &responseObject.Data.Attributes, nil
}

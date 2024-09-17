// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package net

import "github.com/pkg/errors"

const (
	settingsRequestType string = "ci_app_test_service_libraries_settings"
	settingsUrlPath     string = "api/v2/libraries/tests/services/setting"
)

type (
	settingsRequest struct {
		Data settingsRequestHeader `json:"data,omitempty"`
	}

	settingsRequestHeader struct {
		ID         string              `json:"id,omitempty"`
		Type       string              `json:"type,omitempty"`
		Attributes SettingsRequestData `json:"attributes,omitempty"`
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
			ID         string               `json:"id,omitempty"`
			Type       string               `json:"type,omitempty"`
			Attributes SettingsResponseData `json:"attributes,omitempty"`
		} `json:"data,omitempty"`
	}

	SettingsResponseData struct {
		CodeCoverage        bool `json:"code_coverage,omitempty"`
		EarlyFlakeDetection struct {
			Enabled         bool `json:"enabled,omitempty"`
			SlowTestRetries struct {
				One0S   int `json:"10s,omitempty"`
				Three0S int `json:"30s,omitempty"`
				FiveM   int `json:"5m,omitempty"`
				FiveS   int `json:"5s,omitempty"`
			} `json:"slow_test_retries,omitempty"`
			FaultySessionThreshold int `json:"faulty_session_threshold,omitempty"`
		} `json:"early_flake_detection,omitempty"`
		FlakyTestRetriesEnabled bool `json:"flaky_test_retries_enabled,omitempty"`
		ItrEnabled              bool `json:"itr_enabled,omitempty"`
		RequireGit              bool `json:"require_git,omitempty"`
		TestsSkipping           bool `json:"tests_skipping,omitempty"`
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
				RepositoryURL:  c.repositoryUrl,
				Branch:         c.branchName,
				Sha:            c.commitSha,
				Configurations: c.testConfigurations,
			},
		},
	}

	response, err := c.handler.SendRequest(*c.getPostRequestConfig(settingsUrlPath, body))
	if err != nil {
		return nil, errors.Wrap(err, "sending get settings request")
	}

	var responseObject settingsResponse
	err = response.Unmarshal(&responseObject)
	if err != nil {
		return nil, errors.Wrap(err, "unmarshalling settings response")
	}

	return &responseObject.Data.Attributes, nil
}

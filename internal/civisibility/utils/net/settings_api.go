// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

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
		Configurations testConfigurations `json:"configurations"`
	}

	settingsResponse struct {
		Data struct {
			ID         string               `json:"id"`
			Type       string               `json:"type"`
			Attributes SettingsResponseData `json:"attributes"`
		} `json:"data"`
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
		KnownTestsEnabled       bool `json:"known_tests_enabled"`
		ImpactedTestsEnabled    bool `json:"impacted_tests_enabled"`
		TestManagement          struct {
			Enabled             bool `json:"enabled"`
			AttemptToFixRetries int  `json:"attempt_to_fix_retries"`
		} `json:"test_management"`
		SubtestFeaturesEnabled bool `json:"-"`
	}
)

// GetSettings loads settings from the Bazel manifest cache when present and otherwise falls back to the live settings endpoint.
func (c *client) GetSettings() (*SettingsResponseData, error) {
	if bazel.IsManifestModeEnabled() {
		if cachedResponse, ok := loadSettingsFromManifestCache(); ok {
			return cachedResponse, nil
		}
		// Compatible with Bazel offline mode: if cache is missing or invalid, features are disabled.
		log.Debug("civisibility.settings: returning empty settings because manifest cache is unavailable or invalid")
		return &SettingsResponseData{}, nil
	}

	if c.repositoryURL == "" || c.commitSha == "" {
		return nil, fmt.Errorf("civisibility.GetSettings: repository URL and commit SHA are required")
	}

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
		return nil, fmt.Errorf("sending get settings request: %s", err)
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		telemetry.GitRequestsSettingsErrors(telemetry.GetErrorTypeFromStatusCode(response.StatusCode))
	}

	if log.DebugEnabled() {
		log.Debug("civisibility.settings: %s", string(response.Body))
	}

	var responseObject settingsResponse
	err = response.Unmarshal(&responseObject)
	if err != nil {
		return nil, fmt.Errorf("unmarshalling settings response: %s", err)
	}
	logSettingsFeatures(&responseObject.Data.Attributes)

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
	if responseObject.Data.Attributes.FlakyTestRetriesEnabled {
		settingsResponseType = append(settingsResponseType, telemetry.FlakyTestRetriesEnabledSettingsResponseType...)
	}
	if responseObject.Data.Attributes.TestManagement.Enabled {
		settingsResponseType = append(settingsResponseType, telemetry.TestManagementEnabledSettingsResponseType...)
	}
	telemetry.GitRequestsSettingsResponse(settingsResponseType)
	return &responseObject.Data.Attributes, nil
}

// loadSettingsFromManifestCache reads and validates the Bazel manifest cache file for settings.
// It returns the cached settings only when the cache path resolves, the file can be read, and the JSON is valid.
func loadSettingsFromManifestCache() (*SettingsResponseData, bool) {
	cacheFile, ok := bazel.CacheHTTPFile("settings.json")
	if !ok {
		log.Debug("civisibility.settings: manifest mode enabled but settings cache path could not be resolved")
		return nil, false
	}

	cacheFileForLog := bazel.TestOptimizationPathForLog(cacheFile)
	log.Debug("civisibility.settings: reading %s", cacheFileForLog)

	raw, err := os.ReadFile(cacheFile)
	if err != nil {
		log.Debug("civisibility.settings: cannot read settings file %s: %s", cacheFileForLog, err.Error())
		return nil, false
	}

	log.Debug("civisibility.settings: read %s (%d bytes)", cacheFileForLog, len(raw))

	var cachedResponse settingsResponse
	if err := json.Unmarshal(raw, &cachedResponse); err != nil {
		log.Debug("civisibility.settings: invalid settings file %s: %s", cacheFileForLog, err.Error())
		return nil, false
	}

	log.Debug("civisibility.settings: loaded settings from %s", cacheFileForLog)
	logSettingsFeatures(&cachedResponse.Data.Attributes)
	return &cachedResponse.Data.Attributes, true
}

func logSettingsFeatures(settings *SettingsResponseData) {
	if settings == nil {
		return
	}
	log.Debug("civisibility.settings: enabled features [code_coverage:%t itr:%t tests_skipping:%t known_tests:%t impacted_tests:%t early_flake_detection:%t flaky_test_retries:%t test_management:%t require_git:%t attempt_to_fix_retries:%d]",
		settings.CodeCoverage,
		settings.ItrEnabled,
		settings.TestsSkipping,
		settings.KnownTestsEnabled,
		settings.ImpactedTestsEnabled,
		settings.EarlyFlakeDetection.Enabled,
		settings.FlakyTestRetriesEnabled,
		settings.TestManagement.Enabled,
		settings.RequireGit,
		settings.TestManagement.AttemptToFixRetries,
	)
}

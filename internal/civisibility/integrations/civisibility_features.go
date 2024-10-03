// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package integrations

import (
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/constants"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils/net"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

const (
	DefaultFlakyRetryCount      = 5
	DefaultFlakyTotalRetryCount = 1_000
)

type (
	// FlakyRetriesSetting struct to hold all the settings related to flaky tests retries
	FlakyRetriesSetting struct {
		RetryCount               int64
		TotalRetryCount          int64
		RemainingTotalRetryCount int64
	}
)

var (
	// additionalFeaturesInitializationOnce ensures we do the additional features initialization just once
	additionalFeaturesInitializationOnce sync.Once

	// ciVisibilityRapidClient contains the http rapid client to do CI Visibility queries and upload to the rapid backend
	ciVisibilityClient net.Client

	// ciVisibilitySettings contains the CI Visibility settings for this session
	ciVisibilitySettings net.SettingsResponseData

	// ciVisibilityEarlyFlakyDetectionSettings contains the CI Visibility Early Flake Detection data for this session
	ciVisibilityEarlyFlakyDetectionSettings net.EfdResponseData

	// ciVisibilityFlakyRetriesSettings contains the CI Visibility Flaky Retries settings for this session
	ciVisibilityFlakyRetriesSettings FlakyRetriesSetting
)

// ensureAdditionalFeaturesInitialization initialize all the additional features
func ensureAdditionalFeaturesInitialization(serviceName string) {
	additionalFeaturesInitializationOnce.Do(func() {
		// Create the CI Visibility client
		ciVisibilityClient = net.NewClientWithServiceName(serviceName)
		if ciVisibilityClient == nil {
			log.Error("CI Visibility: error getting the ci visibility http client")
			return
		}

		// Get the CI Visibility settings payload for this test session
		ciSettings, err := ciVisibilityClient.GetSettings()
		if err != nil {
			log.Error("CI Visibility: error getting CI visibility settings: %v", err)
		} else if ciSettings != nil {
			ciVisibilitySettings = *ciSettings
		}

		// if early flake detection is enabled then we run the early flake detection request
		if ciVisibilitySettings.EarlyFlakeDetection.Enabled {
			ciEfdData, err := ciVisibilityClient.GetEarlyFlakeDetectionData()
			if err != nil {
				log.Error("CI Visibility: error getting CI visibility early flake detection data: %v", err)
			} else if ciEfdData != nil {
				ciVisibilityEarlyFlakyDetectionSettings = *ciEfdData
			}
		}

		// if flaky test retries is enabled then let's load the flaky retries settings
		if ciVisibilitySettings.FlakyTestRetriesEnabled {
			flakyRetryEnabledByEnv := internal.BoolEnv(constants.CIVisibilityFlakyRetryEnabledEnvironmentVariable, true)
			if flakyRetryEnabledByEnv {
				totalRetriesCount := (int64)(internal.IntEnv(constants.CIVisibilityTotalFlakyRetryCountEnvironmentVariable, DefaultFlakyTotalRetryCount))
				ciVisibilityFlakyRetriesSettings = FlakyRetriesSetting{
					RetryCount:               (int64)(internal.IntEnv(constants.CIVisibilityFlakyRetryCountEnvironmentVariable, DefaultFlakyRetryCount)),
					TotalRetryCount:          totalRetriesCount,
					RemainingTotalRetryCount: totalRetriesCount,
				}
			} else {
				log.Warn("CI Visibility: flaky test retries was disabled by the environment variable")
				ciVisibilitySettings.FlakyTestRetriesEnabled = false
			}
		}
	})
}

// GetSettings gets the settings from the backend settings endpoint
func GetSettings() *net.SettingsResponseData {
	// call to ensure the additional features initialization is completed (service name can be null here)
	ensureAdditionalFeaturesInitialization("")
	return &ciVisibilitySettings
}

// GetEarlyFlakeDetectionSettings gets the early flake detection known tests data
func GetEarlyFlakeDetectionSettings() *net.EfdResponseData {
	// call to ensure the additional features initialization is completed (service name can be null here)
	ensureAdditionalFeaturesInitialization("")
	return &ciVisibilityEarlyFlakyDetectionSettings
}

// GetFlakyRetriesSettings gets the flaky retries settings
func GetFlakyRetriesSettings() *FlakyRetriesSetting {
	// call to ensure the additional features initialization is completed (service name can be null here)
	ensureAdditionalFeaturesInitialization("")
	return &ciVisibilityFlakyRetriesSettings
}

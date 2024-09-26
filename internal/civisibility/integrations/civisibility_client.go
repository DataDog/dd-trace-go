// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package integrations

import (
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils/net"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

var (
	// ciVisibilityRapidClient contains the http rapid client to do CI Visibility querys and upload to the rapid backend
	ciVisibilityClient net.Client

	// ciVisibilitySettings contains the CI Visibility settings for this session
	ciVisibilitySettings *net.SettingsResponseData

	// ciVisibilityEfdData contains the CI Visibility Early Flake Detection data for this session
	ciVisibilityEfdData *net.EfdResponseData
)

func initializeBackendRequests(serviceName string) chan struct{} {
	// Create the CI Visibility client
	ciVisibilityClient = net.NewClientWithServiceName(serviceName)

	// channel to wait for the settings request and (if required) the early flake detection request
	settingsAndEfdChannel := make(chan struct{})
	go func() {
		// Get the CI Visibility settings payload for this test session
		var err error
		ciVisibilitySettings, err = ciVisibilityClient.GetSettings()
		if err != nil {
			log.Error("error getting CI visibility settings: %v", err)
			ciVisibilitySettings = &net.SettingsResponseData{}
		}

		// if early flake detection is enabled then we run the early flake detection request
		if ciVisibilitySettings.EarlyFlakeDetection.Enabled {
			ciVisibilityEfdData, err = ciVisibilityClient.GetEarlyFlakeDetectionData()
			if err != nil {
				log.Error("error getting CI visibility early flake detection data: %v", err)
				ciVisibilityEfdData = &net.EfdResponseData{}
			}
		} else {
			ciVisibilityEfdData = &net.EfdResponseData{}
		}

		settingsAndEfdChannel <- struct{}{}
	}()

	return settingsAndEfdChannel
}

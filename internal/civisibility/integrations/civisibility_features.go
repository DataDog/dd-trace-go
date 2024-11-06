// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package integrations

import (
	"fmt"
	"os"
	"slices"
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/constants"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils"
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

	searchCommitsResponse struct {
		LocalCommits  []string
		RemoteCommits []string
		IsOk          bool
	}
)

var (
	// settingsInitializationOnce ensures we do the settings initialization just once
	settingsInitializationOnce sync.Once

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

	// ciVisibilitySkippables contains the CI Visibility skippable tests for this session
	ciVisibilitySkippables map[string]map[string][]net.SkippableResponseDataAttributes
)

func ensureSettingsInitialization(serviceName string) {
	settingsInitializationOnce.Do(func() {
		log.Debug("civisibility: initializing settings")

		// Create the CI Visibility client
		ciVisibilityClient = net.NewClientWithServiceName(serviceName)
		if ciVisibilityClient == nil {
			log.Error("civisibility: error getting the ci visibility http client")
			return
		}

		// upload the repository changes
		var uploadChannel = make(chan struct{})
		go func() {
			bytes, err := uploadRepositoryChanges()
			if err != nil {
				log.Error("civisibility: error uploading repository changes: %v", err)
			} else {
				log.Debug("civisibility: uploaded %v bytes in pack files", bytes)
			}
			uploadChannel <- struct{}{}
		}()

		// Get the CI Visibility settings payload for this test session
		ciSettings, err := ciVisibilityClient.GetSettings()
		if err != nil {
			log.Error("civisibility: error getting CI visibility settings: %v", err)
		} else if ciSettings != nil {
			ciVisibilitySettings = *ciSettings
		}

		// check if we need to wait for the upload to finish and repeat the settings request or we can just continue
		if ciVisibilitySettings.RequireGit {
			log.Debug("civisibility: waiting for the git upload to finish and repeating the settings request")
			<-uploadChannel
			ciSettings, err = ciVisibilityClient.GetSettings()
			if err != nil {
				log.Error("civisibility: error getting CI visibility settings: %v", err)
			} else if ciSettings != nil {
				ciVisibilitySettings = *ciSettings
			}
		} else {
			log.Debug("civisibility: no need to wait for the git upload to finish")
			// Enqueue a close action to wait for the upload to finish before finishing the process
			PushCiVisibilityCloseAction(func() {
				<-uploadChannel
			})
		}
	})
}

// ensureAdditionalFeaturesInitialization initialize all the additional features
func ensureAdditionalFeaturesInitialization(serviceName string) {
	additionalFeaturesInitializationOnce.Do(func() {
		log.Debug("civisibility: initializing additional features")
		ensureSettingsInitialization(serviceName)
		if ciVisibilityClient == nil {
			return
		}

		// if early flake detection is enabled then we run the early flake detection request
		if ciVisibilitySettings.EarlyFlakeDetection.Enabled {
			ciEfdData, err := ciVisibilityClient.GetEarlyFlakeDetectionData()
			if err != nil {
				log.Error("civisibility: error getting CI visibility early flake detection data: %v", err)
			} else if ciEfdData != nil {
				ciVisibilityEarlyFlakyDetectionSettings = *ciEfdData
				log.Debug("civisibility: early flake detection data loaded.")
			}
		}

		// if flaky test retries is enabled then let's load the flaky retries settings
		if ciVisibilitySettings.FlakyTestRetriesEnabled {
			flakyRetryEnabledByEnv := internal.BoolEnv(constants.CIVisibilityFlakyRetryEnabledEnvironmentVariable, true)
			if flakyRetryEnabledByEnv {
				totalRetriesCount := (int64)(internal.IntEnv(constants.CIVisibilityTotalFlakyRetryCountEnvironmentVariable, DefaultFlakyTotalRetryCount))
				retryCount := (int64)(internal.IntEnv(constants.CIVisibilityFlakyRetryCountEnvironmentVariable, DefaultFlakyRetryCount))
				ciVisibilityFlakyRetriesSettings = FlakyRetriesSetting{
					RetryCount:               retryCount,
					TotalRetryCount:          totalRetriesCount,
					RemainingTotalRetryCount: totalRetriesCount,
				}
				log.Debug("civisibility: automatic test retries enabled [retryCount: %v, totalRetryCount: %v]", retryCount, totalRetriesCount)
			} else {
				log.Warn("civisibility: flaky test retries was disabled by the environment variable")
				ciVisibilitySettings.FlakyTestRetriesEnabled = false
			}
		}

		// if ITR is enabled then we do the skippable tests request
		if ciVisibilitySettings.TestsSkipping {
			// get the skippable tests
			correlationId, skippableTests, err := ciVisibilityClient.GetSkippableTests()
			if err != nil {
				log.Error("civisibility: error getting CI visibility skippable tests: %v", err)
			} else if skippableTests != nil {
				log.Debug("civisibility: skippable tests loaded: %d suites", len(skippableTests))
				utils.AddCITags(constants.ItrCorrelationIDTag, correlationId)
				ciVisibilitySkippables = skippableTests
			}
		}
	})
}

// GetSettings gets the settings from the backend settings endpoint
func GetSettings() *net.SettingsResponseData {
	// call to ensure the settings features initialization is completed (service name can be null here)
	ensureSettingsInitialization("")
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

// GetSkippableTests gets the skippable tests from the backend
func GetSkippableTests() map[string]map[string][]net.SkippableResponseDataAttributes {
	// call to ensure the additional features initialization is completed (service name can be null here)
	ensureAdditionalFeaturesInitialization("")
	return ciVisibilitySkippables
}

func uploadRepositoryChanges() (bytes int64, err error) {
	// get the search commits response
	initialCommitData, err := getSearchCommits()
	if err != nil {
		return 0, fmt.Errorf("civisibility: error getting the search commits response: %s", err.Error())
	}

	// let's check if we could retrieve commit data
	if !initialCommitData.IsOk {
		return 0, nil
	}

	// if there are no commits then we don't need to do anything
	if !initialCommitData.hasCommits() {
		log.Debug("civisibility: no commits found")
		return 0, nil
	}

	// If:
	//   - we have local commits
	//   - there are not missing commits (backend has the total number of local commits already)
	// then we are good to go with it, we don't need to check if we need to unshallow or anything and just go with that.
	if initialCommitData.hasCommits() && len(initialCommitData.missingCommits()) == 0 {
		log.Debug("civisibility: initial commit data has everything already, we don't need to upload anything")
		return 0, nil
	}

	// there's some missing commits on the backend, first we need to check if we need to unshallow before sending anything...
	hasBeenUnshallowed, err := utils.UnshallowGitRepository()
	if err != nil || !hasBeenUnshallowed {
		if err != nil {
			log.Warn(err.Error())
		}
		// if unshallowing the repository failed or if there's nothing to unshallow then we try to upload the packfiles from
		// the initial commit data

		// send the pack file with the missing commits
		return sendObjectsPackFile(initialCommitData.LocalCommits[0], initialCommitData.missingCommits(), initialCommitData.RemoteCommits)
	}

	// after unshallowing the repository we need to get the search commits to calculate the missing commits again
	commitsData, err := getSearchCommits()
	if err != nil {
		return 0, fmt.Errorf("civisibility: error getting the search commits response: %s", err.Error())
	}

	// let's check if we could retrieve commit data
	if !initialCommitData.IsOk {
		return 0, nil
	}

	// send the pack file with the missing commits
	return sendObjectsPackFile(commitsData.LocalCommits[0], commitsData.missingCommits(), commitsData.RemoteCommits)
}

// getSearchCommits gets the search commits response with the local and remote commits
func getSearchCommits() (*searchCommitsResponse, error) {
	localCommits := utils.GetLastLocalGitCommitShas()
	if len(localCommits) == 0 {
		log.Debug("civisibility: no local commits found")
		return newSearchCommitsResponse(nil, nil, false), nil
	}

	log.Debug("civisibility: local commits found: %d", len(localCommits))
	remoteCommits, err := ciVisibilityClient.GetCommits(localCommits)
	return newSearchCommitsResponse(localCommits, remoteCommits, true), err
}

// newSearchCommitsResponse creates a new search commits response
func newSearchCommitsResponse(localCommits []string, remoteCommits []string, isOk bool) *searchCommitsResponse {
	return &searchCommitsResponse{
		LocalCommits:  localCommits,
		RemoteCommits: remoteCommits,
		IsOk:          isOk,
	}
}

// hasCommits returns true if the search commits response has commits
func (r *searchCommitsResponse) hasCommits() bool {
	return len(r.LocalCommits) > 0
}

// missingCommits returns the missing commits between the local and remote commits
func (r *searchCommitsResponse) missingCommits() []string {
	var missingCommits []string
	for _, localCommit := range r.LocalCommits {
		if !slices.Contains(r.RemoteCommits, localCommit) {
			missingCommits = append(missingCommits, localCommit)
		}
	}

	return missingCommits
}

func sendObjectsPackFile(commitSha string, commitsToInclude []string, commitsToExclude []string) (bytes int64, err error) {
	// get the pack files to send
	packFiles := utils.CreatePackFiles(commitsToInclude, commitsToExclude)
	if len(packFiles) == 0 {
		log.Debug("civisibility: no pack files to send")
		return 0, nil
	}

	// send the pack files
	log.Debug("civisibility: sending pack file with missing commits. files: %v", packFiles)

	// try to remove the pack files after sending them
	defer func(files []string) {
		// best effort to remove the pack files after sending
		for _, file := range files {
			_ = os.Remove(file)
		}
	}(packFiles)

	// send the pack files
	return ciVisibilityClient.SendPackFiles(commitSha, packFiles)
}

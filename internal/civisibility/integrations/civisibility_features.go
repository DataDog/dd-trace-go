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
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/bazel"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/impactedtests"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
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

	// repositoryUploadHooks captures repository upload test seams for a single upload attempt.
	repositoryUploadHooks struct {
		// uploadRepositoryChanges replaces the full upload path when tests need to bypass git operations.
		uploadRepositoryChanges func() (int64, error)
		// getSearchCommits retrieves the local/remote commit comparison for the current repository state.
		getSearchCommits func() (*searchCommitsResponse, error)
		// unshallowGitRepository expands shallow clones before computing the final missing commits.
		unshallowGitRepository func() (bool, error)
		// sendObjectsPackFile uploads a git packfile containing the missing commits.
		sendObjectsPackFile func(string, []string, []string) (int64, error)
	}
)

var (
	// settingsInitializationOnce ensures we do the settings initialization just once
	settingsInitializationOnce sync.Once

	// additionalFeaturesInitializationOnce ensures we do the additional features initialization just once
	additionalFeaturesInitializationOnce sync.Once

	// additionalFeaturesInitializationMu serializes additional feature initialization with test-only state resets.
	additionalFeaturesInitializationMu sync.Mutex

	// repositoryUploadHooksMu protects repository upload hooks that tests replace while settings upload work may still be running.
	repositoryUploadHooksMu sync.RWMutex

	// ciVisibilityRapidClient contains the http rapid client to do CI Visibility queries and upload to the rapid backend
	ciVisibilityClient net.Client

	// newCIVisibilityClientWithServiceNameFunc creates the CI Visibility client used during settings bootstrap.
	newCIVisibilityClientWithServiceNameFunc = net.NewClientWithServiceName

	// ciVisibilitySettings contains the CI Visibility settings for this session
	ciVisibilitySettings net.SettingsResponseData

	// ciVisibilityKnownTests contains the CI Visibility Known Tests data for this session
	ciVisibilityKnownTests net.KnownTestsResponseData

	// ciVisibilityFlakyRetriesSettings contains the CI Visibility Flaky Retries settings for this session
	ciVisibilityFlakyRetriesSettings FlakyRetriesSetting

	// ciVisibilitySkippables contains the CI Visibility skippable tests for this session
	ciVisibilitySkippables map[string]map[string][]net.SkippableResponseDataAttributes
	// ciVisibilitySkippablesResponse contains the full skippable-tests response for this session.
	ciVisibilitySkippablesResponse *net.SkippableTestsResponse

	// ciVisibilityTestManagementTests contains the CI Visibility test management tests for this session
	ciVisibilityTestManagementTests net.TestManagementTestsResponseDataModules

	// ciVisibilityImpactedTestsAnalyzer contains the CI Visibility impacted tests analyzer
	ciVisibilityImpactedTestsAnalyzer *impactedtests.ImpactedTestAnalyzer

	// uploadRepositoryChangesFunc is a must-not-call test seam used to prove offline/file modes suppress git upload. A nil value uses the default git upload path.
	uploadRepositoryChangesFunc func() (int64, error)

	// getSearchCommitsFunc allows tests to exercise repository upload control flow without reading local git state.
	getSearchCommitsFunc = getSearchCommits

	// unshallowGitRepositoryFunc allows tests to control the fallback branch without mutating the local repository.
	unshallowGitRepositoryFunc = utils.UnshallowGitRepository

	// sendObjectsPackFileFunc allows tests to inspect the upload request without creating or sending real pack files.
	sendObjectsPackFileFunc = sendObjectsPackFile
)

// ensureSettingsInitialization performs the one-time settings bootstrap, including any git upload work required before a final settings read.
func ensureSettingsInitialization(serviceName string) {
	if isProcessRetryChild() {
		return
	}
	settingsInitializationOnce.Do(func() {
		log.Debug("civisibility: initializing settings")
		defer log.Debug("civisibility: settings initialization complete")

		// Create the CI Visibility client
		ciVisibilityClient = newCIVisibilityClientWithServiceNameFunc(serviceName)
		if ciVisibilityClient == nil {
			log.Error("civisibility: error getting the ci visibility http client")
			return
		}

		testOptimizationMode := bazel.CurrentMode()
		var uploadChannel = make(chan struct{})
		gitUploadEnabled := internal.BoolEnv(constants.CIVisibilityGitUploadEnabledEnvironmentVariable, true)
		uploadEnabled := gitUploadEnabled && !testOptimizationMode.ManifestEnabled && !testOptimizationMode.PayloadFilesEnabled
		log.Debug("civisibility: settings initialization mode [manifest:%t payload_files:%t manifest_file:%s payload_root:%s git_upload_enabled:%t repository_upload_enabled:%t]",
			testOptimizationMode.ManifestEnabled, testOptimizationMode.PayloadFilesEnabled, bazel.TestOptimizationPathForLog(testOptimizationMode.ManifestPath), testOptimizationMode.PayloadsRoot, gitUploadEnabled, uploadEnabled)
		if uploadEnabled {
			repositoryUpload := snapshotRepositoryUploadHooks()

			// upload the repository changes
			go func() {
				defer func() {
					close(uploadChannel)
				}()
				bytes, err := repositoryUpload.run()
				if err != nil {
					log.Error("civisibility: error uploading repository changes: %s", err.Error())
				} else {
					log.Debug("civisibility: uploaded %d bytes in pack files", bytes)
				}
			}()
		} else {
			close(uploadChannel)
			if gitUploadEnabled {
				log.Debug("civisibility: repository upload disabled for current test optimization mode")
			} else {
				log.Debug("civisibility: repository upload disabled by environment variable")
			}
		}

		//Wait for the upload with timeout func
		waitUpload := func(timeout time.Duration) bool {
			select {
			case <-uploadChannel:
				// All ok, upload succeeded
				return true
			case <-time.After(timeout):
				log.Warn("civisibility: timeout waiting for upload repository changes")
				return false
			}
		}
		// returns a closure suitable for PushCiVisibilityCloseAction that will wait
		// for the upload to complete (or time out) using the given timeout.
		waitUploadFactory := func(timeout time.Duration) func() {
			return func() { waitUpload(timeout) }
		}

		// Get the CI Visibility settings payload for this test session
		ciSettings, err := ciVisibilityClient.GetSettings()
		if err != nil || ciSettings == nil {
			logSettingsFetchError(err)
			if uploadEnabled {
				log.Debug("civisibility: no need to wait for the git upload to finish")
				// Enqueue a close action to wait for the upload to finish before finishing the process
				PushCiVisibilityCloseAction(waitUploadFactory(time.Minute))
			} else {
				log.Debug("civisibility: no upload wait required")
			}
			return
		}

		// check if we need to wait for the upload to finish and repeat the settings request or we can just continue
		if uploadEnabled && ciSettings.RequireGit {
			log.Debug("civisibility: waiting for the git upload to finish and repeating the settings request")
			if !waitUpload(1 * time.Minute) {
				log.Error("civisibility: error getting CI visibility settings due to timeout")
				return
			}
			ciSettings, err = ciVisibilityClient.GetSettings()
			if err != nil || ciSettings == nil {
				logSettingsFetchError(err)
				return
			}
		}

		// check if we need to disable EFD because known tests is not enabled
		if !ciSettings.KnownTestsEnabled {
			// "known_tests_enabled" parameter works as a kill-switch for EFD, so if “known_tests_enabled” is false it
			// will disable EFD even if “early_flake_detection.enabled” is set to true (which should not happen normally,
			// the backend should disable both of them in that case)
			ciSettings.EarlyFlakeDetection.Enabled = false
		}

		// check if flaky test retries is disabled by env-vars
		if ciSettings.FlakyTestRetriesEnabled && !internal.BoolEnv(constants.CIVisibilityFlakyRetryEnabledEnvironmentVariable, true) {
			log.Warn("civisibility: flaky test retries was disabled by the environment variable")
			ciSettings.FlakyTestRetriesEnabled = false
		}

		// check if impacted tests is disabled by env-vars
		if ciSettings.ImpactedTestsEnabled && !internal.BoolEnv(constants.CIVisibilityImpactedTestsDetectionEnabled, true) {
			log.Warn("civisibility: impacted tests was disabled by the environment variable")
			ciSettings.ImpactedTestsEnabled = false
		}

		// check if code coverage report upload is disabled by env-vars
		if ciSettings.CoverageReportUploadEnabled && !internal.BoolEnv(constants.CIVisibilityCodeCoverageReportUploadEnabledEnvironmentVariable, true) {
			log.Warn("civisibility: code coverage report upload was disabled by the environment variable")
			ciSettings.CoverageReportUploadEnabled = false
		}

		// check if test management is disabled by env-vars
		if ciSettings.TestManagement.Enabled && !internal.BoolEnv(constants.CIVisibilityTestManagementEnabledEnvironmentVariable, true) {
			log.Warn("civisibility: test management was disabled by the environment variable")
			ciSettings.TestManagement.Enabled = false
		}

		// overwrite the test management attempt to fix retries with the env var if set
		testManagementAttemptToFixRetriesEnv := internal.IntEnv(constants.CIVisibilityTestManagementAttemptToFixRetriesEnvironmentVariable, -1)
		if testManagementAttemptToFixRetriesEnv != -1 {
			ciSettings.TestManagement.AttemptToFixRetries = testManagementAttemptToFixRetriesEnv
		}

		if testOptimizationMode.ManifestEnabled {
			if ciSettings.TestsSkipping {
				log.Debug("civisibility: test skipping disabled in manifest mode")
			}
			ciSettings.TestsSkipping = false
		}

		// payload-file mode must avoid impacted-tests git workflows.
		if testOptimizationMode.PayloadFilesEnabled {
			log.Debug("civisibility: impacted tests disabled in payload-file mode")
			ciSettings.ImpactedTestsEnabled = false
		}

		// determine if subtest-specific features are enabled via environment variables
		subtestFeaturesEnabled := internal.BoolEnv(constants.CIVisibilitySubtestFeaturesEnabled, true)
		if !subtestFeaturesEnabled {
			log.Debug("civisibility: subtest test management features disabled by environment variable")
		}
		ciSettings.SubtestFeaturesEnabled = subtestFeaturesEnabled

		// check if we need to wait for the upload to finish before continuing
		if uploadEnabled {
			if ciSettings.ImpactedTestsEnabled {
				log.Debug("civisibility: impacted tests is enabled we need to wait for the upload to finish (for the unshallow process)")
				waitUpload(30 * time.Second)
			} else {
				log.Debug("civisibility: no need to wait for the git upload to finish")
				// Enqueue a close action to wait for the upload to finish before finishing the process
				PushCiVisibilityCloseAction(waitUploadFactory(time.Minute))
			}
		} else {
			log.Debug("civisibility: no upload wait required")
		}

		// set the ciVisibilitySettings with the settings from the backend
		ciVisibilitySettings = *ciSettings
	})
}

// logSettingsFetchError reports a failed or empty CI Visibility settings response.
func logSettingsFetchError(err error) {
	if err != nil {
		log.Error("civisibility: error getting CI visibility settings: %s", err.Error())
		return
	}
	log.Error("civisibility: error getting CI visibility settings: empty response")
}

// ensureAdditionalFeaturesInitialization loads CI Visibility features that depend on the previously fetched settings.
func ensureAdditionalFeaturesInitialization(_ string) {
	if isProcessRetryChild() {
		return
	}
	additionalFeaturesInitializationMu.Lock()
	defer additionalFeaturesInitializationMu.Unlock()

	additionalFeaturesInitializationOnce.Do(func() {
		log.Debug("civisibility: initializing additional features")
		defer log.Debug("civisibility: additional features initialization complete")

		// get a copy of the settings instance
		currentSettings := *GetSettings()

		// if there's no ciVisibilityClient then we don't need to do anything
		if ciVisibilityClient == nil {
			return
		}

		// map to store the additional tags we want to add (Capabilities and CorrelationId)
		additionalTags := make(map[string]string)
		defer func() {
			if len(additionalTags) > 0 {
				log.Debug("civisibility: adding additional tags: %v", additionalTags) //nolint:gocritic // Map structure logging for debugging
				utils.AddCITagsMap(additionalTags)
			}
		}()

		// set the default values for the additional tags
		additionalTags[constants.LibraryCapabilitiesEarlyFlakeDetection] = "1"
		additionalTags[constants.LibraryCapabilitiesAutoTestRetries] = "1"
		additionalTags[constants.LibraryCapabilitiesCoverageReportUpload] = "1"
		additionalTags[constants.LibraryCapabilitiesTestImpactAnalysis] = "1"
		additionalTags[constants.LibraryCapabilitiesTestManagementQuarantine] = "1"
		additionalTags[constants.LibraryCapabilitiesTestManagementDisable] = "1"
		additionalTags[constants.LibraryCapabilitiesTestManagementAttemptToFix] = "5"

		// mutex to protect the additional tags map
		var aTagsMutex sync.Mutex
		// function to set additional tags locking with the mutex
		setAdditionalTags := func(key string, value string) {
			aTagsMutex.Lock()
			defer aTagsMutex.Unlock()
			additionalTags[key] = value
		}

		// if flaky test retries is enabled then let's load the flaky retries settings
		if currentSettings.FlakyTestRetriesEnabled {
			totalRetriesCount := (int64)(internal.IntEnv(constants.CIVisibilityTotalFlakyRetryCountEnvironmentVariable, DefaultFlakyTotalRetryCount))
			retryCount := (int64)(internal.IntEnv(constants.CIVisibilityFlakyRetryCountEnvironmentVariable, DefaultFlakyRetryCount))
			ciVisibilityFlakyRetriesSettings = FlakyRetriesSetting{
				RetryCount:               retryCount,
				TotalRetryCount:          totalRetriesCount,
				RemainingTotalRetryCount: totalRetriesCount,
			}
			log.Debug("civisibility: automatic test retries enabled [retryCount: %d, totalRetryCount: %d]", retryCount, totalRetriesCount)
		}

		// wait group to wait for all the additional features to be loaded
		var wg sync.WaitGroup

		// if early flake detection is enabled then we run the known tests request
		if currentSettings.KnownTestsEnabled {
			wg.Go(func() {
				ciEfdData, err := ciVisibilityClient.GetKnownTests()
				if err != nil {
					log.Error("civisibility: error getting CI visibility known tests data: %s", err.Error())
				} else if ciEfdData != nil {
					ciVisibilityKnownTests = *ciEfdData
					log.Debug("civisibility: known tests data loaded.")
				}
			})
		}

		// if ITR is enabled then we do the skippable tests request
		if currentSettings.TestsSkipping {
			wg.Go(func() {
				// get the skippable tests
				response, err := ciVisibilityClient.GetSkippableTests()
				if err != nil {
					log.Error("civisibility: error getting CI visibility skippable tests: %s", err.Error())
				} else if response != nil {
					log.Debug("civisibility: skippable tests loaded: %d suites", len(response.Skippables))
					setAdditionalTags(constants.ItrCorrelationIDTag, response.CorrelationID)
					ciVisibilitySkippables = response.Skippables
					ciVisibilitySkippablesResponse = response
				}
			})
		}

		// if test management is enabled then we do the test management request
		if currentSettings.TestManagement.Enabled {
			wg.Go(func() {
				testManagementTests, err := ciVisibilityClient.GetTestManagementTests()
				if err != nil {
					log.Error("civisibility: error getting CI visibility test management tests: %s", err.Error())
				} else if testManagementTests != nil {
					ciVisibilityTestManagementTests = *testManagementTests
					log.Debug("civisibility: test management loaded [attemptToFixRetries: %d]", currentSettings.TestManagement.AttemptToFixRetries)
				}
			})
		}

		// if wheter the settings response or the env var is true we load the impacted tests analyzer
		if currentSettings.ImpactedTestsEnabled {
			wg.Go(func() {
				iTests, err := impactedtests.NewImpactedTestAnalyzer()
				if err != nil {
					log.Error("civisibility: error getting CI visibility impacted tests analyzer: %s", err.Error())
				} else {
					ciVisibilityImpactedTestsAnalyzer = iTests
					log.Debug("civisibility: impacted tests analyzer loaded")
				}
			})
		}

		// wait for all the additional features to be loaded
		wg.Wait()
	})
}

// GetSettings gets the settings from the backend settings endpoint
func GetSettings() *net.SettingsResponseData {
	if isProcessRetryChild() {
		return &net.SettingsResponseData{}
	}
	// call to ensure the settings features initialization is completed (service name can be null here)
	ensureSettingsInitialization("")
	return &ciVisibilitySettings
}

// GetKnownTests gets the known tests data
func GetKnownTests() *net.KnownTestsResponseData {
	if isProcessRetryChild() {
		return &net.KnownTestsResponseData{}
	}
	// call to ensure the additional features initialization is completed (service name can be null here)
	ensureAdditionalFeaturesInitialization("")
	return &ciVisibilityKnownTests
}

// GetTestManagementTestsData gets the test management tests data
func GetTestManagementTestsData() *net.TestManagementTestsResponseDataModules {
	if isProcessRetryChild() {
		return &net.TestManagementTestsResponseDataModules{}
	}
	// call to ensure the additional features initialization is completed (service name can be null here)
	ensureAdditionalFeaturesInitialization("")
	return &ciVisibilityTestManagementTests
}

// GetFlakyRetriesSettings gets the flaky retries settings
func GetFlakyRetriesSettings() *FlakyRetriesSetting {
	if isProcessRetryChild() {
		return &FlakyRetriesSetting{}
	}
	// call to ensure the additional features initialization is completed (service name can be null here)
	ensureAdditionalFeaturesInitialization("")
	return &ciVisibilityFlakyRetriesSettings
}

// GetSkippableTests gets the skippable tests from the backend
func GetSkippableTests() map[string]map[string][]net.SkippableResponseDataAttributes {
	if isProcessRetryChild() {
		return nil
	}
	// call to ensure the additional features initialization is completed (service name can be null here)
	ensureAdditionalFeaturesInitialization("")
	return ciVisibilitySkippables
}

// GetSkippableTestsResponse gets the full skippable-tests response from the backend.
func GetSkippableTestsResponse() *net.SkippableTestsResponse {
	if isProcessRetryChild() {
		return nil
	}
	// call to ensure the additional features initialization is completed (service name can be null here)
	ensureAdditionalFeaturesInitialization("")
	return ciVisibilitySkippablesResponse
}

// GetImpactedTestsAnalyzer gets the impacted tests analyzer
func GetImpactedTestsAnalyzer() *impactedtests.ImpactedTestAnalyzer {
	if isProcessRetryChild() {
		return nil
	}
	// call to ensure the additional features initialization is completed (service name can be null here)
	ensureAdditionalFeaturesInitialization("")
	return ciVisibilityImpactedTestsAnalyzer
}

// snapshotRepositoryUploadHooks returns a stable hook set so asynchronous upload work is isolated from test resets.
func snapshotRepositoryUploadHooks() repositoryUploadHooks {
	repositoryUploadHooksMu.RLock()
	defer repositoryUploadHooksMu.RUnlock()

	return repositoryUploadHooks{
		uploadRepositoryChanges: uploadRepositoryChangesFunc,
		getSearchCommits:        getSearchCommitsFunc,
		unshallowGitRepository:  unshallowGitRepositoryFunc,
		sendObjectsPackFile:     sendObjectsPackFileFunc,
	}
}

// run executes either a full upload replacement hook or the default upload path with the captured hooks.
func (hooks repositoryUploadHooks) run() (int64, error) {
	if hooks.uploadRepositoryChanges != nil {
		return hooks.uploadRepositoryChanges()
	}
	return uploadRepositoryChangesWithHooks(hooks)
}

// uploadRepositoryChanges discovers the commits and packfiles that must be uploaded so backend features can reason about the current repo state.
func uploadRepositoryChanges() (bytes int64, err error) {
	return uploadRepositoryChangesWithHooks(snapshotRepositoryUploadHooks())
}

// uploadRepositoryChangesWithHooks runs the default git upload flow against a stable hook snapshot.
func uploadRepositoryChangesWithHooks(hooks repositoryUploadHooks) (bytes int64, err error) {
	// get the search commits response
	initialCommitData, err := hooks.getSearchCommits()
	if err != nil {
		return 0, fmt.Errorf("civisibility: error getting the search commits response: %s", err)
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

	// Calculate the initial missing commits once and reuse the same ordered list if
	// the repository cannot be unshallowed. missingCommits walks the local and
	// remote commit lists, so calling it twice here would repeat identical work.
	initialMissingCommits := initialCommitData.missingCommits()

	// If there are not missing commits (backend has the total number of local commits already), then we are good to go
	// with it, we don't need to check if we need to unshallow or anything and just go with that.
	if len(initialMissingCommits) == 0 {
		log.Debug("civisibility: initial commit data has everything already, we don't need to upload anything")
		return 0, nil
	}

	// there's some missing commits on the backend, first we need to check if we need to unshallow before sending anything...
	hasBeenUnshallowed, err := hooks.unshallowGitRepository()
	if err != nil || !hasBeenUnshallowed {
		if err != nil {
			log.Warn("%s", err.Error())
		}
		// if unshallowing the repository failed or if there's nothing to unshallow then we try to upload the packfiles from
		// the initial commit data

		// send the pack file with the missing commits
		return hooks.sendObjectsPackFile(initialCommitData.LocalCommits[0], initialMissingCommits, initialCommitData.RemoteCommits)
	}

	// after unshallowing the repository we need to get the search commits to calculate the missing commits again
	commitsData, err := hooks.getSearchCommits()
	if err != nil {
		return 0, fmt.Errorf("civisibility: error getting the search commits response: %s", err)
	}

	// let's check if we could retrieve commit data
	if !commitsData.IsOk {
		return 0, nil
	}

	// send the pack file with the missing commits
	return hooks.sendObjectsPackFile(commitsData.LocalCommits[0], commitsData.missingCommits(), commitsData.RemoteCommits)
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

// sendObjectsPackFile uploads one packfile chunk for the requested commit graph slice and reports the number of bytes sent.
func sendObjectsPackFile(commitSha string, commitsToInclude []string, commitsToExclude []string) (bytes int64, err error) {
	// get the pack files to send
	packFiles := utils.CreatePackFiles(commitsToInclude, commitsToExclude)
	if len(packFiles) == 0 {
		log.Debug("civisibility: no pack files to send")
		return 0, nil
	}

	// send the pack files
	log.Debug("civisibility: sending pack file with missing commits. files: %v", packFiles) //nolint:gocritic // File list logging for debugging

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

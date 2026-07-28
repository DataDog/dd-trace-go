// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package integrations

import (
	"sync"

	"github.com/DataDog/dd-trace-go/v2/internal/bazel"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
	internaltelemetry "github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

func resetCIVisibilityStateForTesting() {
	stopCIVisibilitySignalHandler()

	additionalFeaturesInitializationMu.Lock()
	defer additionalFeaturesInitializationMu.Unlock()

	// Payload-file tests can start a telemetry client that writes files
	// asynchronously, so stop it before temporary output directories are cleaned.
	internaltelemetry.StopApp()

	settingsInitializationOnce = sync.Once{}
	additionalFeaturesInitializationOnce = sync.Once{}

	closeActions = nil

	ciVisibilityClient = nil
	ciVisibilitySettings = net.SettingsResponseData{}
	ciVisibilityKnownTests = net.KnownTestsResponseData{}
	ciVisibilityFlakyRetriesSettings = FlakyRetriesSetting{}
	ciVisibilitySkippables = nil
	ciVisibilitySkippablesResponse = nil
	ciVisibilityTestManagementTests = net.TestManagementTestsResponseDataModules{}
	ciVisibilityImpactedTestsAnalyzer = nil
	sourceFileMetadataCache = sync.Map{}

	repositoryUploadHooksMu.Lock()
	uploadRepositoryChangesFunc = nil
	// Return "no commits" so the upload goroutine spawned by ensureSettingsInitialization
	// exits immediately without reading ciVisibilityClient, preventing a data race with the
	// next reset call that sets ciVisibilityClient = nil.
	// Tests that need real commit data override getSearchCommitsFunc themselves.
	getSearchCommitsFunc = func() (*searchCommitsResponse, error) {
		return newSearchCommitsResponse(nil, nil, false), nil
	}
	unshallowGitRepositoryFunc = utils.UnshallowGitRepository
	sendObjectsPackFileFunc = sendObjectsPackFile
	repositoryUploadHooksMu.Unlock()

	newCIVisibilityClientWithServiceNameFunc = net.NewClientWithServiceName

	utils.ResetCITags()
	utils.ResetCIMetrics()
	utils.ResetCodeOwnersForTesting()
	bazel.ResetForTesting()
}

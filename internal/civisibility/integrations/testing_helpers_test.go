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
)

func resetCIVisibilityStateForTesting() {
	additionalFeaturesInitializationMu.Lock()
	defer additionalFeaturesInitializationMu.Unlock()

	settingsInitializationOnce = sync.Once{}
	additionalFeaturesInitializationOnce = sync.Once{}

	closeActions = nil

	ciVisibilityClient = nil
	ciVisibilitySettings = net.SettingsResponseData{}
	ciVisibilityKnownTests = net.KnownTestsResponseData{}
	ciVisibilityFlakyRetriesSettings = FlakyRetriesSetting{}
	ciVisibilitySkippables = nil
	ciVisibilityTestManagementTests = net.TestManagementTestsResponseDataModules{}
	ciVisibilityImpactedTestsAnalyzer = nil
	sourceFileMetadataCache = sync.Map{}

	uploadRepositoryChangesFunc = uploadRepositoryChanges
	newCIVisibilityClientWithServiceNameFunc = net.NewClientWithServiceName
	// Return "no commits" so the upload goroutine spawned by ensureSettingsInitialization
	// exits immediately without reading ciVisibilityClient, preventing a data race with the
	// next reset call that sets ciVisibilityClient = nil.
	// Tests that need real commit data override getSearchCommitsFunc themselves.
	getSearchCommitsFunc = func() (*searchCommitsResponse, error) {
		return newSearchCommitsResponse(nil, nil, false), nil
	}
	unshallowGitRepositoryFunc = utils.UnshallowGitRepository
	sendObjectsPackFileFunc = sendObjectsPackFile

	utils.ResetCITags()
	utils.ResetCIMetrics()
	utils.ResetCodeOwnersForTesting()
	bazel.ResetForTesting()
}

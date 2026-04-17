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
	settingsInitializationOnce = sync.Once{}

	closeActions = nil

	ciVisibilityClient = nil
	ciVisibilitySettings = net.SettingsResponseData{}
	ciVisibilityKnownTests = net.KnownTestsResponseData{}
	ciVisibilityFlakyRetriesSettings = FlakyRetriesSetting{}
	ciVisibilitySkippables = nil
	ciVisibilityTestManagementTests = net.TestManagementTestsResponseDataModules{}
	ciVisibilityImpactedTestsAnalyzer = nil

	uploadRepositoryChangesFunc = uploadRepositoryChanges

	utils.ResetCITags()
	utils.ResetCIMetrics()
	bazel.ResetForTesting()
}

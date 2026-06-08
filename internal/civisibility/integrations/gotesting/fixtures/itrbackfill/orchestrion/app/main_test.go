// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package app

import (
	"os"
	"testing"

	"github.com/DataDog/orchestrion/runtime/built"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting/fixtures/itrbackfill/internal/mockci"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/filebitmap"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
)

const orchestrionLibPath = "internal/civisibility/integrations/gotesting/fixtures/itrbackfill/orchestrion/lib/lib.go"

type fixtureScenario struct {
	tests                   []mockci.SkippableTest
	coverage                map[string][]byte
	expectSkipped           bool
	expectCoverage          bool
	expectCoverageRequests  *bool
	expectSkippableRequests int
	expectSkippingEnabled   string
}

func TestMain(m *testing.M) {
	if os.Getenv("DD_ITR_BACKFILL_FIXTURE") != "1" {
		os.Exit(m.Run())
	}
	if !built.WithOrchestrion {
		panic("expected fixture to run with Orchestrion")
	}

	settings := net.SettingsResponseData{
		ItrEnabled:              true,
		TestsSkipping:           true,
		CodeCoverage:            os.Getenv("DD_ITR_BACKFILL_CODE_COVERAGE") != "false",
		RequireGit:              false,
		FlakyTestRetriesEnabled: false,
		KnownTestsEnabled:       false,
		ImpactedTestsEnabled:    false,
		SubtestFeaturesEnabled:  false,
	}
	scenario := orchestrionScenario()
	server := mockci.Start(settings, scenario.tests, scenario.coverage)
	defer server.Close()

	exitCode := m.Run()
	if server.SkippableRequests() != scenario.expectSkippableRequests {
		panic("unexpected skippable request count")
	}
	skippedByITR := server.HasEventMeta("TestCoversLib", constants.TestSkippedByITR, "true")
	if skippedByITR != scenario.expectSkipped {
		panic("unexpected ITR skip decision")
	}
	if value, ok := server.SessionMeta(constants.ITRTestsSkippingEnabled); !ok || value != scenario.expectSkippingEnabled {
		panic("unexpected ITR tests skipping enabled tag")
	}
	coverageValue, hasCoverage := server.SessionCoverage(constants.CodeCoveragePercentageOfTotalLines)
	if hasCoverage != scenario.expectCoverage || (scenario.expectCoverage && coverageValue <= 0) {
		panic("expected corrected session coverage")
	}
	if scenario.expectCoverageRequests != nil && (server.CoverageRequests() > 0) != *scenario.expectCoverageRequests {
		panic("unexpected coverage upload request count")
	}
	os.Exit(exitCode)
}

func orchestrionScenario() fixtureScenario {
	validCoverage := map[string][]byte{
		orchestrionLibPath: filebitmap.FromActiveRange(1, 64).ToArray(),
	}
	defaultScenario := fixtureScenario{
		tests: []mockci.SkippableTest{
			{Suite: "app_test.go", Name: "TestCoversLib"},
		},
		coverage:                validCoverage,
		expectSkipped:           true,
		expectCoverage:          true,
		expectSkippableRequests: 1,
		expectSkippingEnabled:   "true",
	}

	switch os.Getenv("DD_ITR_BACKFILL_SCENARIO") {
	case "", "positive", "atomic", "no-coverprofile":
		return defaultScenario
	case "codecoverage-disabled":
		expectCoverageRequests := false
		defaultScenario.expectCoverageRequests = &expectCoverageRequests
		return defaultScenario
	case "missing-line":
		defaultScenario.tests = []mockci.SkippableTest{
			{Suite: "app_test.go", Name: "TestCoversLib", MissingLineCodeCoverage: true},
		}
		defaultScenario.expectSkipped = false
		return defaultScenario
	case "missing-coverage":
		defaultScenario.coverage = nil
		defaultScenario.expectSkipped = false
		defaultScenario.expectSkippingEnabled = "false"
		return defaultScenario
	case "unmatched-coverage":
		defaultScenario.coverage = map[string][]byte{
			"internal/civisibility/integrations/gotesting/fixtures/itrbackfill/orchestrion/lib/other.go": filebitmap.FromActiveRange(1, 64).ToArray(),
		}
		defaultScenario.expectSkipped = false
		defaultScenario.expectSkippingEnabled = "false"
		return defaultScenario
	case "narrowing-run", "unsupported-set", "no-skippable":
		if os.Getenv("DD_ITR_BACKFILL_SCENARIO") == "no-skippable" {
			defaultScenario.tests = nil
		} else {
			defaultScenario.expectSkippingEnabled = "false"
		}
		defaultScenario.expectSkipped = false
		return defaultScenario
	default:
		panic("unknown ITR backfill fixture scenario")
	}
}

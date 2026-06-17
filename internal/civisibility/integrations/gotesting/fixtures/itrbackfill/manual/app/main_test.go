// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package app

import (
	"os"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting/fixtures/itrbackfill/internal/mockci"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/filebitmap"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
)

const manualLibPath = "internal/civisibility/integrations/gotesting/fixtures/itrbackfill/manual/lib/lib.go"

func TestMain(m *testing.M) {
	if os.Getenv("DD_ITR_BACKFILL_FIXTURE") != "1" {
		os.Exit(m.Run())
	}

	scenario := os.Getenv("DD_ITR_BACKFILL_SCENARIO")
	settings := net.SettingsResponseData{
		ItrEnabled:              true,
		TestsSkipping:           true,
		CodeCoverage:            os.Getenv("DD_ITR_BACKFILL_CODE_COVERAGE") != "false",
		RequireGit:              false,
		FlakyTestRetriesEnabled: os.Getenv("DD_ITR_BACKFILL_FLAKY_RETRY") == "true",
	}
	expectCoverageRequests := os.Getenv("DD_ITR_BACKFILL_CODE_COVERAGE") != "false"
	expectSkipped := true
	expectSkippingEnabled := "true"
	expectPositiveCoverage := true
	coverage := map[string][]byte{
		manualLibPath: filebitmap.FromActiveRange(1, 64).ToArray(),
	}
	tests := []mockci.SkippableTest{
		{Suite: "app_test.go", Name: "TestCoversLib"},
	}
	if os.Getenv("DD_ITR_BACKFILL_PARTIAL_COVERAGE") == "true" {
		coverage["internal/civisibility/integrations/gotesting/fixtures/itrbackfill/manual/lib/unmatched.go"] = filebitmap.FromActiveRange(1, 64).ToArray()
		expectPositiveCoverage = false
	}
	if scenario == "manual-producer-bitmap-upload" {
		tests = nil
		coverage = nil
		expectSkipped = false
		expectPositiveCoverage = true
		expectCoverageRequests = true
	}
	server := mockci.Start(settings, tests, coverage)
	defer server.Close()

	exitCode := gotesting.RunM(m)
	if server.SkippableRequests() != 1 {
		panic("expected exactly one skippable request")
	}
	skippedByITR := server.HasEventMeta("TestCoversLib", constants.TestSkippedByITR, "true")
	if skippedByITR != expectSkipped {
		panic("unexpected ITR skip decision")
	}
	if value, ok := server.SessionMeta(constants.ITRTestsSkippingEnabled); !ok || value != expectSkippingEnabled {
		panic("unexpected ITR tests skipping enabled tag")
	}
	coverageValue, hasCoverage := server.SessionCoverage(constants.CodeCoveragePercentageOfTotalLines)
	if !hasCoverage || (expectPositiveCoverage && coverageValue <= 0) {
		panic("unexpected session coverage")
	}
	if !expectCoverageRequests && server.CoverageRequests() > 0 {
		panic("unexpected coverage upload request count")
	}
	if scenario == "manual-producer-bitmap-upload" {
		if server.CoverageRequests() == 0 {
			panic("expected coverage upload request")
		}
		uploaded := server.UploadedCoverage()
		if len(uploaded) != 1 {
			panic("unexpected uploaded coverage bitmap file count")
		}
		actual := uploaded[manualLibPath]
		if len(actual) == 0 {
			panic("expected uploaded coverage bitmap for manual lib")
		}
		expected := filebitmap.FromActiveRange(1, 64).ToArray()
		if !filebitmap.NewFileBitmapFromBytes(actual).IntersectsWith(filebitmap.NewFileBitmapFromBytes(expected)) {
			panic("uploaded coverage bitmap did not intersect expected lines for manual lib")
		}
	}
	if server.EventTypeCount(constants.SpanTypeTestModule) != 1 {
		panic("expected exactly one test module event")
	}
	if server.EventTypeCount(constants.SpanTypeTestSuite) != 1 {
		panic("expected exactly one test suite event")
	}
	os.Exit(exitCode)
}

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

	settings := net.SettingsResponseData{
		ItrEnabled:              true,
		TestsSkipping:           true,
		CodeCoverage:            os.Getenv("DD_ITR_BACKFILL_CODE_COVERAGE") != "false",
		RequireGit:              false,
		FlakyTestRetriesEnabled: false,
	}
	expectCoverageRequests := os.Getenv("DD_ITR_BACKFILL_CODE_COVERAGE") != "false"
	server := mockci.Start(settings, []mockci.SkippableTest{
		{Suite: "app_test.go", Name: "TestCoversLib"},
	}, map[string][]byte{
		manualLibPath: filebitmap.FromActiveRange(1, 64).ToArray(),
	})
	defer server.Close()

	exitCode := gotesting.RunM(m)
	if server.SkippableRequests() != 1 {
		panic("expected exactly one skippable request")
	}
	if !server.HasEventMeta("TestCoversLib", constants.TestSkippedByITR, "true") {
		panic("expected TestCoversLib to be skipped by ITR")
	}
	if coverageValue, ok := server.SessionCoverage(constants.CodeCoveragePercentageOfTotalLines); !ok || coverageValue <= 0 {
		panic("expected corrected session coverage")
	}
	if !expectCoverageRequests && server.CoverageRequests() > 0 {
		panic("unexpected coverage upload request count")
	}
	os.Exit(exitCode)
}

//go:build fuzzexamplefixture

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package fuzzexample

import (
	"fmt"
	"os"
	"reflect"
	"runtime"
	"testing"

	"github.com/DataDog/orchestrion/runtime/built"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting/fixtures/itrbackfill/internal/mockci"
	civisibilityutils "github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
)

type eventExpectation struct {
	name        string
	status      string
	finalStatus string
	tag         string
}

func TestMain(m *testing.M) {
	if os.Getenv("DD_FUZZ_EXAMPLE_MODE") == "" {
		os.Exit(m.Run())
	}
	if fuzzWorkerFromArgs() {
		os.Exit(m.Run())
	}
	if !built.WithOrchestrion {
		panic("expected fixture to run with Orchestrion")
	}

	scenario := os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO")
	settings := net.SettingsResponseData{
		RequireGit:             false,
		KnownTestsEnabled:      false,
		ImpactedTestsEnabled:   false,
		SubtestFeaturesEnabled: true,
	}
	var management *net.TestManagementTestsResponseDataModules
	if scenario == "test-management" {
		settings.TestManagement.Enabled = true
		management = fuzzExampleTestManagementData()
	}
	server := mockci.StartWithTestManagement(settings, nil, nil, management)
	defer server.Close()

	exitCode, panicData := runFixtureM(m.Run)
	wantExitCode := 0
	want := []eventExpectation{
		{name: "FuzzNativeParity/seed#0", status: constants.TestStatusPass},
		{name: "FuzzNativeParity/seed#1", status: constants.TestStatusPass},
		{name: "FuzzNativeParity", status: constants.TestStatusPass},
		{name: "FuzzNativeSkip/seed#0", status: constants.TestStatusSkip},
		{name: "FuzzNativeSkip", status: constants.TestStatusPass},
		{name: "ExampleNativeParity", status: constants.TestStatusPass},
		{name: "ExampleNativeUnordered", status: constants.TestStatusPass},
		{name: "ExampleNativeMismatch", status: constants.TestStatusPass},
		{name: "ExampleNativePanic", status: constants.TestStatusPass},
		{name: "FuzzNativeTypes/seed#0", status: constants.TestStatusPass},
		{name: "FuzzNativeTypes", status: constants.TestStatusPass},
	}
	if scenario == "active-fuzz" {
		want = []eventExpectation{
			{name: "FuzzActiveOther/seed#0", status: constants.TestStatusPass},
			{name: "FuzzActiveOther", status: constants.TestStatusPass},
			{name: "FuzzNativeParity", status: constants.TestStatusPass},
		}
	} else if scenario == "filtered" {
		want = []eventExpectation{{name: "TestNormalSelection", status: constants.TestStatusPass}}
	} else if scenario == "seed-lifecycle" {
		wantExitCode = 1
		want = []eventExpectation{
			{name: "FuzzSeedCleanupFailure/seed#0", status: constants.TestStatusFail},
			{name: "FuzzSeedCleanupFailure", status: constants.TestStatusFail},
			{name: "FuzzSeedCleanupSkip/seed#0", status: constants.TestStatusSkip},
			{name: "FuzzSeedCleanupSkip", status: constants.TestStatusPass},
			{name: "FuzzSeedParallelFailure/seed#0/parallel", status: constants.TestStatusFail},
			{name: "FuzzSeedParallelFailure/seed#0", status: constants.TestStatusFail},
			{name: "FuzzSeedParallelFailure", status: constants.TestStatusFail},
		}
	} else if scenario == "fuzz-missing-call" {
		wantExitCode = 1
		want = []eventExpectation{{name: "FuzzMissingCall", status: constants.TestStatusFail}}
	} else if scenario == "example-panic-nil" {
		want = []eventExpectation{{name: "ExamplePanicNil", status: constants.TestStatusFail}}
	} else if scenario == "test-management" {
		want = managedEventExpectations()
	}
	switch scenario {
	case "fuzz-failure":
		wantExitCode = 1
		want[1].status = constants.TestStatusFail
		want[2].status = constants.TestStatusFail
	case "example-mismatch":
		wantExitCode = 1
		want[7].status = constants.TestStatusFail
	case "example-panic":
		want[8].status = constants.TestStatusFail
	}

	if scenario == "example-panic" {
		if fmt.Sprint(panicData) != "example panic sentinel" {
			panic(fmt.Sprintf("unexpected example panic: %v", panicData))
		}
	} else if scenario == "example-panic-nil" {
		if fmt.Sprint(panicData) != "test executed panic(nil) or runtime.Goexit" {
			panic(fmt.Sprintf("unexpected panic(nil) terminal: %v", panicData))
		}
	} else if panicData != nil {
		panic(panicData)
	} else if exitCode != wantExitCode {
		panic(fmt.Sprintf("unexpected test exit code: got %d, want %d", exitCode, wantExitCode))
	}
	for _, expectation := range want {
		finalStatus := expectation.finalStatus
		if finalStatus == "" {
			finalStatus = expectation.status
		}
		if !server.HasEventResourceSuffixMeta(expectation.name, constants.TestStatus, expectation.status) {
			panic(fmt.Sprintf("missing native event %s with status %s; events: %+v", expectation.name, expectation.status, server.Events()))
		}
		if !server.HasEventResourceSuffixMeta(expectation.name, constants.TestFinalStatus, finalStatus) {
			panic("missing final status for native event " + expectation.name)
		}
		if !server.HasEventResourceSuffixMetaKey(expectation.name, constants.TestSourceFile) {
			panic("missing source metadata for native event " + expectation.name)
		}
		if expectation.tag != "" && !server.HasEventResourceSuffixMeta(expectation.name, expectation.tag, "true") {
			panic("missing Test Management tag for native event " + expectation.name)
		}
	}
	if server.HasEventResourceMeta("ExampleWithoutOutput", "", "") {
		panic("example without an output directive emitted a native event")
	}
	if server.EventTypeCount(constants.SpanTypeTest) != len(want) {
		panic(fmt.Sprintf("unexpected native test event count: got %d, want %d", server.EventTypeCount(constants.SpanTypeTest), len(want)))
	}
	if server.EventTypeCount(constants.SpanTypeTestSession) != 1 || server.EventTypeCount(constants.SpanTypeTestModule) != 1 || server.EventTypeCount(constants.SpanTypeTestSuite) != 1 {
		panic("expected one closed session, module, and suite")
	}
	os.Exit(0)
}

func fuzzExampleTestManagementData() *net.TestManagementTestsResponseDataModules {
	fn := runtime.FuncForPC(reflect.ValueOf(FuzzManagedSeeds).Pointer())
	moduleName, suiteName := civisibilityutils.GetModuleAndSuiteName(fn.Entry())
	tests := map[string]net.TestManagementTestsResponseDataTestProperties{
		"FuzzManagedSeeds/seed#0":         {Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{Disabled: true}},
		"FuzzManagedSeeds/seed#1":         {Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{Quarantined: true}},
		"FuzzManagedSeeds/seed#2":         {Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{AttemptToFix: true}},
		"FuzzManagedDisabled":             {Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{Disabled: true}},
		"FuzzManagedQuarantined":          {Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{Quarantined: true}},
		"FuzzManagedAttemptToFix":         {Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{AttemptToFix: true}},
		"ExampleManagedDisabled":          {Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{Disabled: true}},
		"ExampleManagedQuarantined":       {Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{Quarantined: true}},
		"ExampleManagedQuarantinedPanic":  {Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{Quarantined: true}},
		"ExampleManagedQuarantinedGoexit": {Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{Quarantined: true}},
		"ExampleManagedAttemptToFix":      {Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{AttemptToFix: true}},
	}
	return &net.TestManagementTestsResponseDataModules{Modules: map[string]net.TestManagementTestsResponseDataSuites{
		moduleName: {Suites: map[string]net.TestManagementTestsResponseDataTests{
			suiteName: {Tests: tests},
		}},
	}}
}

func managedEventExpectations() []eventExpectation {
	return []eventExpectation{
		{name: "FuzzManagedSeeds/seed#0", status: constants.TestStatusSkip, finalStatus: constants.TestStatusSkip, tag: constants.TestIsDisabled},
		{name: "FuzzManagedSeeds/seed#1", status: constants.TestStatusFail, finalStatus: constants.TestStatusSkip, tag: constants.TestIsQuarantined},
		{name: "FuzzManagedSeeds/seed#2", status: constants.TestStatusPass, finalStatus: constants.TestStatusPass, tag: constants.TestIsAttempToFix},
		{name: "FuzzManagedSeeds", status: constants.TestStatusPass},
		{name: "FuzzManagedDisabled", status: constants.TestStatusSkip, finalStatus: constants.TestStatusSkip, tag: constants.TestIsDisabled},
		{name: "FuzzManagedQuarantined", status: constants.TestStatusFail, finalStatus: constants.TestStatusSkip, tag: constants.TestIsQuarantined},
		{name: "FuzzManagedAttemptToFix/seed#0", status: constants.TestStatusPass, finalStatus: constants.TestStatusPass, tag: constants.TestIsAttempToFix},
		{name: "FuzzManagedAttemptToFix", status: constants.TestStatusPass, finalStatus: constants.TestStatusPass, tag: constants.TestIsAttempToFix},
		{name: "ExampleManagedDisabled", status: constants.TestStatusSkip, finalStatus: constants.TestStatusSkip, tag: constants.TestIsDisabled},
		{name: "ExampleManagedQuarantined", status: constants.TestStatusFail, finalStatus: constants.TestStatusSkip, tag: constants.TestIsQuarantined},
		{name: "ExampleManagedQuarantinedPanic", status: constants.TestStatusFail, finalStatus: constants.TestStatusSkip, tag: constants.TestIsQuarantined},
		{name: "ExampleManagedQuarantinedGoexit", status: constants.TestStatusFail, finalStatus: constants.TestStatusSkip, tag: constants.TestIsQuarantined},
		{name: "ExampleManagedAttemptToFix", status: constants.TestStatusPass, finalStatus: constants.TestStatusPass, tag: constants.TestIsAttempToFix},
	}
}

func runFixtureM(run func() int) (exitCode int, panicData any) {
	func() {
		defer func() { panicData = recover() }()
		exitCode = run()
	}()
	return exitCode, panicData
}

func fuzzWorkerFromArgs() bool {
	for _, arg := range os.Args {
		if arg == "-test.fuzzworker" || arg == "-test.fuzzworker=true" {
			return true
		}
	}
	return false
}

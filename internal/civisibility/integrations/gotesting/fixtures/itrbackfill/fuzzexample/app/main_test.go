// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package app

import (
	"fmt"
	"os"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting/fixtures/itrbackfill/internal/mockci"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
)

type eventExpectation struct {
	name   string
	status string
}

func TestMain(m *testing.M) {
	mode := os.Getenv("DD_FUZZ_EXAMPLE_MODE")
	if mode == "" {
		os.Exit(m.Run())
	}
	if fuzzWorkerFromArgs() {
		_ = os.Setenv("DD_CIVISIBILITY_ENABLED", "false")
		os.Exit(m.Run())
	}
	server := mockci.Start(net.SettingsResponseData{
		RequireGit:             false,
		KnownTestsEnabled:      false,
		ImpactedTestsEnabled:   false,
		SubtestFeaturesEnabled: true,
	}, nil, nil)
	defer server.Close()

	exitCode, panicData := runFixtureM(func() int { return gotesting.RunM(m) })

	scenario := os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO")
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
		want = []eventExpectation{{name: "FuzzNativeParity", status: constants.TestStatusPass}}
	} else if scenario == "filtered" {
		want = []eventExpectation{{name: "TestNormalSelection", status: constants.TestStatusPass}}
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
	} else if panicData != nil {
		panic(panicData)
	} else if exitCode != wantExitCode {
		panic(fmt.Sprintf("unexpected test exit code: got %d, want %d", exitCode, wantExitCode))
	}
	for _, expectation := range want {
		if !server.HasEventResourceMeta(expectation.name, constants.TestStatus, expectation.status) {
			panic(fmt.Sprintf("missing native event %s with status %s", expectation.name, expectation.status))
		}
		if !server.HasEventResourceMeta(expectation.name, constants.TestFinalStatus, expectation.status) {
			panic("missing final status for native event " + expectation.name)
		}
		if !server.HasEventResourceMetaKey(expectation.name, constants.TestSourceFile) {
			panic("missing source metadata for native event " + expectation.name)
		}
	}
	if server.HasEventResourceMeta("ExampleWithoutOutput", "", "") {
		panic("example without an output directive emitted a native event")
	}
	if server.EventTypeCount(constants.SpanTypeTest) != len(want) {
		panic(fmt.Sprintf("unexpected native test event count: got %d, want %d", server.EventTypeCount(constants.SpanTypeTest), len(want)))
	}
	if server.EventTypeCount(constants.SpanTypeTestModule) != 1 || server.EventTypeCount(constants.SpanTypeTestSuite) != 1 {
		panic("expected one closed module and suite")
	}

	// The controller validates native failure scenarios itself, so a verified
	// expected failure must not make the outer regression test fail.
	os.Exit(0)
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

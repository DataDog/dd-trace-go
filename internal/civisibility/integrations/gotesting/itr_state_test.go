// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"os"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
)

func exerciseITRCoverageBackfillState(t *testing.T) {
	previousState := activeITRState.Load()
	t.Cleanup(func() {
		activeITRState.Store(previousState)
	})

	state := newExerciseITRState()
	skipDecision := state.decisionFor(exerciseTestInfo("TestSafeSkip"), &testExecutionMetadata{}, false)
	if !skipDecision.skip || skipDecision.forcedRun {
		t.Fatal("expected safe coverage candidate to be skipped")
	}
	state.markActualSkip()

	state.validateCoverageBackfillScope([]*testingTInfo{
		exerciseTestInfo("TestSafeSkip"),
		exerciseTestInfo("TestForcedRun"),
		exerciseTestInfo("TestMissingCoverage"),
		exerciseTestInfo("TestMixedCoverage"),
	})
	if !state.coverageBackfillReady || state.disabledReason != "" {
		t.Fatalf("expected in-process skippable response to stay safe, ready=%t reason=%q", state.coverageBackfillReady, state.disabledReason)
	}

	forcedDecision := state.decisionFor(exerciseTestInfo("TestForcedRun"), &testExecutionMetadata{}, true)
	if forcedDecision.skip || !forcedDecision.forcedRun {
		t.Fatal("expected unskippable coverage candidate to be forced to run")
	}

	state = newExerciseITRState()
	state.validateCoverageBackfillScope([]*testingTInfo{exerciseTestInfo("TestSafeSkip")})
	if !state.coverageBackfillReady || state.disabledReason != "" {
		t.Fatalf("expected out-of-process skippable candidates to be ignored, ready=%t reason=%q", state.coverageBackfillReady, state.disabledReason)
	}
	scopeDecision := state.decisionFor(exerciseTestInfo("TestSafeSkip"), &testExecutionMetadata{}, false)
	if !scopeDecision.skip || scopeDecision.forcedRun {
		t.Fatal("expected in-process safe candidate to remain skippable")
	}

	state = newExerciseITRState()
	state.response.Skippables["other_suite_test.go"] = map[string][]net.SkippableResponseDataAttributes{
		"TestOutsideProcess": {
			{Suite: "other_suite_test.go", Name: "TestOutsideProcess", Parameters: `{"case":"one"}`},
		},
	}
	state.validateCoverageBackfillScope([]*testingTInfo{exerciseTestInfo("TestSafeSkip")})
	if !state.coverageBackfillReady || state.disabledReason != "" {
		t.Fatalf("expected out-of-process parameters to be ignored, ready=%t reason=%q", state.coverageBackfillReady, state.disabledReason)
	}

	state = newExerciseITRState()
	state.response.Skippables["suite_test.go"]["TestSafeSkip"][0].Parameters = `{"case":"one"}`
	state.validateCoverageBackfillScope([]*testingTInfo{
		exerciseTestInfo("TestSafeSkip"),
		exerciseTestInfo("TestForcedRun"),
		exerciseTestInfo("TestMissingCoverage"),
		exerciseTestInfo("TestMixedCoverage"),
	})
	if state.coverageBackfillReady {
		t.Fatal("expected parameterized skippable response to disable coverage backfill")
	}
	if state.disabledReason != itrBackfillReasonParameterized {
		t.Fatalf("expected parameterized reason, got %q", state.disabledReason)
	}
	parameterizedDecision := state.decisionFor(exerciseTestInfo("TestSafeSkip"), &testExecutionMetadata{}, false)
	if parameterizedDecision.skip || parameterizedDecision.forcedRun {
		t.Fatal("expected parameterized skippable candidate not to match the non-parameterized test")
	}
	safeDecisionWithParameterizedResponse := state.decisionFor(exerciseTestInfo("TestForcedRun"), &testExecutionMetadata{}, false)
	if !safeDecisionWithParameterizedResponse.skip || safeDecisionWithParameterizedResponse.forcedRun {
		t.Fatal("expected unrelated safe candidate to remain skippable when backfill is disabled by parameters")
	}

	state = newExerciseITRState()
	missingDecision := state.decisionFor(exerciseTestInfo("TestMissingCoverage"), &testExecutionMetadata{}, false)
	if missingDecision.skip || missingDecision.forcedRun {
		t.Fatal("expected missing-line-coverage candidate to run without a forced-run tag")
	}

	mixedDecision := state.decisionFor(exerciseTestInfo("TestMixedCoverage"), &testExecutionMetadata{}, false)
	if mixedDecision.skip || mixedDecision.forcedRun {
		t.Fatal("expected mixed missing/non-missing candidate to run")
	}
}

func exerciseNarrowingFlagParsing(t *testing.T) {
	previousArgs := os.Args
	t.Cleanup(func() {
		os.Args = previousArgs
	})

	shortCases := []struct {
		args []string
		want bool
	}{
		{args: []string{"-short"}, want: true},
		{args: []string{"-short=true"}, want: true},
		{args: []string{"-short=false"}, want: false},
		{args: []string{"-test.short=0"}, want: false},
		{args: []string{"--short=false"}, want: false},
		{args: []string{"-short=not-bool"}, want: true},
	}
	for _, tc := range shortCases {
		os.Args = append([]string{"gotesting.test"}, tc.args...)
		if got := testFlagSetFromArgs("test.short"); got != tc.want {
			t.Fatalf("test.short args %v: expected %t, got %t", tc.args, tc.want, got)
		}
	}

	os.Args = []string{"gotesting.test", "-run=TestFoo"}
	if !testFlagSetFromArgs("test.run") {
		t.Fatal("expected explicit -run to be treated as narrowing")
	}

	os.Args = []string{"gotesting.test", "--", "-run=TestFoo"}
	if testFlagSetFromArgs("test.run") {
		t.Fatal("expected args after -- to be ignored")
	}
}

func newExerciseITRState() *itrState {
	return &itrState{
		settings:              &net.SettingsResponseData{ItrEnabled: true, TestsSkipping: true},
		coverageActive:        true,
		coverageBackfillReady: true,
		response: &net.SkippableTestsResponse{
			Skippables: map[string]map[string][]net.SkippableResponseDataAttributes{
				"suite_test.go": {
					"TestSafeSkip": {
						{Suite: "suite_test.go", Name: "TestSafeSkip"},
					},
					"TestForcedRun": {
						{Suite: "suite_test.go", Name: "TestForcedRun"},
					},
					"TestMissingCoverage": {
						{Suite: "suite_test.go", Name: "TestMissingCoverage", MissingLineCodeCoverage: true},
					},
					"TestMixedCoverage": {
						{Suite: "suite_test.go", Name: "TestMixedCoverage", MissingLineCodeCoverage: true},
						{Suite: "suite_test.go", Name: "TestMixedCoverage"},
					},
				},
			},
		},
	}
}

func exerciseTestInfo(testName string) *testingTInfo {
	return &testingTInfo{
		commonInfo: commonInfo{
			moduleName: "module",
			suiteName:  "suite_test.go",
			testName:   testName,
			identity:   newTestIdentity("module", "suite_test.go", testName),
		},
	}
}

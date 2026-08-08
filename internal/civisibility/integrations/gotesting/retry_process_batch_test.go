// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
)

func TestProcessRetryBatchConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "batch.json")
	want := &processRetryBatchConfig{
		Version:                processRetryBatchVersion,
		AttemptToFixRetries:    3,
		CollectPerTestCoverage: true,
		ITRCoverageActive:      true,
		ImpactedTestsEnabled:   true,
		Tests: []processRetryBatchTestConfig{
			{
				TestName:             "TestA",
				InvocationOrdinal:    11,
				DisabledSubtests:     []string{"TestA/disabled"},
				AttemptToFixSubtests: []string{"TestA/atf"},
				ITRSubtests: []processRetrySubtestITRConfig{{
					TestName:                "TestA/skipped",
					MissingLineCodeCoverage: true,
				}},
			},
			{TestName: "TestB", InvocationOrdinal: 12},
		},
	}

	require.NoError(t, writeProcessRetryBatchConfig(path, want))
	requireProcessRetryFileMode(t, path, processRetryBatchManifestMode)
	got, err := readProcessRetryBatchConfig(path)
	require.NoError(t, err)
	require.Equal(t, want, got)
	child := processRetryBatchChildConfig(processRetryChildConfig{
		ResultPath: path, Attempt: 1, RetryReason: processRetryBatchReason, MRunEpoch: 7, Batch: got,
	}, 0, got.Tests[0])
	require.Equal(t, "TestA", child.TestName)
	require.Equal(t, 1, child.Attempt)
	require.Equal(t, processRetryBatchReason, child.RetryReason)
	require.Equal(t, uint64(7), child.MRunEpoch)
	require.Equal(t, uint64(11), child.InvocationOrdinal)
	require.True(t, child.CollectPerTestCoverage)
	require.True(t, child.itrCoverageActive)
	require.Equal(t, 3, child.attemptToFixRetries)
	require.Equal(t, []string{"TestA/disabled"}, child.batchTest.DisabledSubtests)
	require.Equal(t, []string{"TestA/atf"}, child.batchTest.AttemptToFixSubtests)
}

func TestProcessRetryNativeScheduledChildConfigUsesBatchIdentityAndGates(t *testing.T) {
	root := processRetryChildConfig{
		ResultPath:        filepath.Join(t.TempDir(), "result.json"),
		Attempt:           1,
		RetryReason:       processRetryBatchReason,
		MRunEpoch:         7,
		InvocationOrdinal: 11,
		Batch: &processRetryBatchConfig{
			Version:                processRetryBatchVersion,
			PreserveNativeSchedule: true,
		},
	}

	child := processRetryBatchChildConfig(root, 2, processRetryBatchTestConfig{TestName: "TestA"})

	require.Zero(t, child.MRunEpoch)
	require.Zero(t, child.InvocationOrdinal)
	require.Equal(t, processRetryBatchGatePath(root.ResultPath, 2), child.nativeGatePath)
	require.Equal(t, processRetryBatchParallelPath(root.ResultPath, 2), child.nativeParallelPath)
	require.Equal(t, processRetryBatchEnumerationPath(root.ResultPath), child.nativeEnumerationPath)
}

func TestProcessRetryBatchInvocationStateRoundTrip(t *testing.T) {
	path := processRetryBatchGatePath(filepath.Join(t.TempDir(), "result.json"), 0)
	workingDirectory := t.TempDir()
	baseline := &processRetryLaunchBaseline{
		workingDirectory: workingDirectory,
		environment:      []string{"FIRST=one", "SECOND=two=three"},
	}

	require.NoError(t, writeProcessRetryBatchInvocationState(path, baseline))
	requireProcessRetryFileMode(t, path, processRetryBatchManifestMode)
	state, run, err := waitForProcessRetryBatchGate(path, time.Time{}, false)
	require.NoError(t, err)
	require.True(t, run)
	require.Equal(t, processRetryBatchInvocationState{
		Version:          processRetryBatchGateVersion,
		WorkingDirectory: workingDirectory,
		Environment:      baseline.environment,
	}, state)
}

func TestCurrentProcessRetryBatchInvocationStateRoundTrip(t *testing.T) {
	t.Setenv("DD_TEST_BATCH_PARENT_STATE", "post-enumeration")
	path := filepath.Join(t.TempDir(), "parent-state.json")
	workingDirectory, err := os.Getwd()
	require.NoError(t, err)

	require.NoError(t, writeCurrentProcessRetryBatchInvocationState(path))
	state, err := readProcessRetryBatchInvocationState(path)
	require.NoError(t, err)
	require.Equal(t, workingDirectory, state.WorkingDirectory)
	require.Contains(t, state.Environment, "DD_TEST_BATCH_PARENT_STATE=post-enumeration")
}

func TestApplyProcessRetryBatchFinalStatePreservesCoverageDirectory(t *testing.T) {
	for _, test := range []struct {
		name             string
		apply            func(processRetryBatchInvocationState) error
		wantCoverage     bool
		wantCIVisibility string
	}{
		{name: "child invocation", apply: applyProcessRetryBatchInvocationState, wantCIVisibility: "false"},
		{name: "parent final state", apply: applyProcessRetryBatchFinalState, wantCoverage: true, wantCIVisibility: "parent"},
	} {
		t.Run(test.name, func(t *testing.T) {
			workingDirectory, err := os.Getwd()
			require.NoError(t, err)
			t.Setenv(processRetryCoverageDirectoryEnvironmentVariable, filepath.Join(t.TempDir(), "coverage"))
			t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "parent")
			t.Setenv("DD_TEST_BATCH_REMOVED_STATE", "remove-me")
			environment := slices.DeleteFunc(sanitizeProcessRetryBaseEnv(os.Environ()), func(entry string) bool {
				key, _, _ := strings.Cut(entry, "=")
				return key == "DD_TEST_BATCH_REMOVED_STATE"
			})

			require.NoError(t, test.apply(processRetryBatchInvocationState{
				Version:          processRetryBatchGateVersion,
				WorkingDirectory: workingDirectory,
				Environment:      environment,
			}))
			_, coveragePresent := os.LookupEnv(processRetryCoverageDirectoryEnvironmentVariable)
			require.Equal(t, test.wantCoverage, coveragePresent)
			require.Equal(t, test.wantCIVisibility, os.Getenv(constants.CIVisibilityEnabledEnvironmentVariable))
			_, removedPresent := os.LookupEnv("DD_TEST_BATCH_REMOVED_STATE")
			require.False(t, removedPresent)
		})
	}
}

func TestTransitionNativeScheduledTestToParallelUsesCapturedParentBarrier(t *testing.T) {
	result := make(chan error, 1)
	t.Run("parent", func(parent *testing.T) {
		parent.Run("parallel", func(child *testing.T) {
			result <- transitionNativeScheduledTestToParallel(child)
		})
		fields := getTestPrivateFields(parent)
		require.NotNil(parent, fields)
		barrier := *fields.barrier
		*fields.barrier = nil
		*fields.barrier = barrier
	})
	require.NoError(t, <-result)
}

func TestWaitNativeScheduledFirstAttemptReacquiresProcessSlotBeforeParallelBody(t *testing.T) {
	const stateKey = "DD_TEST_BATCH_CHILD_PRE_PARALLEL_STATE"
	t.Setenv(stateKey, "parent")
	resetProcessRetryLimiterForTesting(t)
	limiter := getProcessRetryLimiter()
	initialSlot := limiter.acquireWithShutdownLimit(context.Background(), nil, nil, 1)
	require.Equal(t, processRetryLimiterAcquired, initialSlot.Cause)
	processSlotRelease := make(chan processRetryLimiterRelease, 1)
	processSlotRelease <- initialSlot.Release
	dir := t.TempDir()
	resultRoot := filepath.Join(dir, "result.json")
	testConfig := processRetryBatchTestConfig{TestName: "TestParallel"}
	acquireEntered := make(chan struct{})
	signalCalled := make(chan struct{})
	activeAtSignal := make(chan int, 1)
	stateAtSignal := make(chan string, 1)
	batchConfig := &processRetryBatchConfig{
		Version:                processRetryBatchVersion,
		Tests:                  []processRetryBatchTestConfig{testConfig},
		PreserveNativeSchedule: true,
	}
	batch := &nativeScheduledProcessRetryBatch{
		rootCfg: processRetryChildConfig{
			TestName:    processRetryBatchTestName,
			Attempt:     1,
			RetryReason: processRetryBatchReason,
			Batch:       batchConfig,
		},
		batch:              batchConfig,
		resultRoot:         resultRoot,
		ctx:                &processRetryObservedDoneContext{Context: t.Context(), entered: acquireEntered},
		done:               make(chan struct{}),
		cancel:             func() {},
		processSlotRelease: processSlotRelease,
	}
	batch.signal = func() error {
		limiter.mu.Lock()
		activeAtSignal <- limiter.active
		limiter.mu.Unlock()
		stateAtSignal <- os.Getenv(stateKey)
		close(signalCalled)
		now := time.Now()
		err := writeProcessRetryResultAtomically(processRetryBatchResultPath(resultRoot, 0), processRetryResult{
			Version:        1,
			TestName:       testConfig.TestName,
			Attempt:        1,
			RetryReason:    processRetryBatchReason,
			Status:         processRetryStatusPass,
			RootParallel:   true,
			StartUnixNano:  now.UnixNano(),
			FinishUnixNano: now.Add(time.Millisecond).UnixNano(),
			DurationNanos:  int64(time.Millisecond),
		})
		close(batch.done)
		return err
	}
	environment := slices.DeleteFunc(sanitizeProcessRetryBaseEnv(os.Environ()), func(entry string) bool {
		key, _, _ := strings.Cut(entry, "=")
		return key == stateKey
	})
	require.NoError(t, writeProcessRetryBatchInvocationState(processRetryBatchParallelPath(resultRoot, 0), &processRetryLaunchBaseline{
		workingDirectory: dir,
		environment:      append(environment, stateKey+"=child"),
	}))
	coordinator := &processRetryCoordinator{
		nativeTestIndex: map[string]int{testConfig.TestName: 0},
		nativeTests:     []processRetryBatchTestConfig{testConfig},
		nativeBatches:   map[uint64]*nativeScheduledProcessRetryBatch{1: batch},
		shutdown:        make(chan struct{}),
	}
	group := &deferredProcessRetryGroup{
		identity:          *newTestIdentity("module", "suite", testConfig.TestName),
		invocationOrdinal: 1,
		launchBaseline: &processRetryLaunchBaseline{
			workingDirectory:  dir,
			maxConcurrency:    1,
			maxConcurrencySet: true,
		},
	}
	result := make(chan processRetryAttemptResult, 1)
	signalBeforeAdmission := make(chan bool, 1)
	t.Run("parent", func(parent *testing.T) {
		parent.Run("parallel", func(child *testing.T) {
			result <- coordinator.waitNativeScheduledFirstAttempt(group, child)
		})
		blockingSlot := limiter.acquireWithShutdownLimit(t.Context(), nil, nil, 1)
		require.Equal(parent, processRetryLimiterAcquired, blockingSlot.Cause)
		go func() {
			select {
			case <-acquireEntered:
				signalBeforeAdmission <- false
			case <-signalCalled:
				signalBeforeAdmission <- true
			}
			blockingSlot.Release()
		}()
	})

	require.False(t, <-signalBeforeAdmission)
	require.Equal(t, 1, <-activeAtSignal)
	require.Equal(t, "child", <-stateAtSignal)
	require.Equal(t, processRetryStatusPass, effectiveProcessRetryStatus(<-result, false).Status)
	require.Zero(t, processRetryLimiterActiveForTesting(t, limiter))
}

func TestProcessRetryBatchInvocationStateRejectsInvalidPayloads(t *testing.T) {
	for name, payload := range map[string]string{
		"unknown field":         `{"version":1,"working_directory":"/tmp","environment":[],"unknown":true}`,
		"trailing data":         `{"version":1,"working_directory":"/tmp","environment":[]} {}`,
		"relative directory":    `{"version":1,"working_directory":"relative","environment":[]}`,
		"private transport":     `{"version":1,"working_directory":"/tmp","environment":["DD_CIVISIBILITY_INTERNAL_RETRY_PROCESS_CHILD=true"]}`,
		"coverage environment":  `{"version":1,"working_directory":"/tmp","environment":["GOCOVERDIR=/tmp/cov"]}`,
		"duplicate environment": `{"version":1,"working_directory":"/tmp","environment":["PATH=one","PATH=two"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "gate.json")
			require.NoError(t, os.WriteFile(path, []byte(payload), processRetryBatchManifestMode))
			_, err := readProcessRetryBatchInvocationState(path)
			require.Error(t, err)
		})
	}
}

func TestNativeScheduledBatchResultSkipsUninvokedTestsBeforeWaiting(t *testing.T) {
	resultPath := filepath.Join(t.TempDir(), "result.json")
	batch := &nativeScheduledProcessRetryBatch{
		batch:      &processRetryBatchConfig{Tests: []processRetryBatchTestConfig{{TestName: "TestA"}, {TestName: "TestB"}}},
		resultRoot: resultPath,
		done:       make(chan struct{}),
		cancel:     func() {},
	}
	require.NoError(t, writeProcessRetryBatchInvocationState(processRetryBatchGatePath(resultPath, 0), &processRetryLaunchBaseline{workingDirectory: t.TempDir()}))
	_, run, err := waitForProcessRetryBatchGate(processRetryBatchGatePath(resultPath, 0), time.Time{}, false)
	require.NoError(t, err)
	require.True(t, run)
	coordinator := newProcessRetryCoordinatorForTesting(false)
	coordinator.nativeBatches = map[uint64]*nativeScheduledProcessRetryBatch{1: batch}
	type gateResult struct {
		run bool
		err error
	}
	gate := make(chan gateResult, 1)
	go func() {
		_, run, err := waitForProcessRetryBatchGate(processRetryBatchGatePath(resultPath, 1), time.Time{}, false)
		gate <- gateResult{run: run, err: err}
		close(batch.done)
	}()

	_, ok := coordinator.nativeScheduledBatchResult(1)
	require.True(t, ok)
	decision := <-gate
	require.NoError(t, decision.err)
	require.False(t, decision.run)
	require.NoFileExists(t, processRetryBatchSkipPath(resultPath, 0))
	require.FileExists(t, processRetryBatchSkipPath(resultPath, 1))
}

func TestProcessRetryBatchConfigRejectsInvalidManifests(t *testing.T) {
	validTest := processRetryBatchTestConfig{TestName: "TestA"}
	tests := map[string]*processRetryBatchConfig{
		"nil":                  nil,
		"wrong version":        {Version: processRetryBatchVersion + 1, Tests: []processRetryBatchTestConfig{validTest}},
		"empty":                {Version: processRetryBatchVersion},
		"missing name":         {Version: processRetryBatchVersion, Tests: []processRetryBatchTestConfig{{InvocationOrdinal: 1}}},
		"duplicate test name":  {Version: processRetryBatchVersion, Tests: []processRetryBatchTestConfig{validTest, validTest}},
		"negative retries":     {Version: processRetryBatchVersion, AttemptToFixRetries: -1, Tests: []processRetryBatchTestConfig{validTest}},
		"too many retries":     {Version: processRetryBatchVersion, AttemptToFixRetries: processRetryBatchMaxTests + 1, Tests: []processRetryBatchTestConfig{validTest}},
		"managed outside test": {Version: processRetryBatchVersion, Tests: []processRetryBatchTestConfig{{TestName: "TestA", AttemptToFixSubtests: []string{"TestB/child"}}}},
		"duplicate managed":    {Version: processRetryBatchVersion, Tests: []processRetryBatchTestConfig{{TestName: "TestA", DisabledSubtests: []string{"TestA/child"}, AttemptToFixSubtests: []string{"TestA/child"}}}},
		"ITR outside test":     {Version: processRetryBatchVersion, Tests: []processRetryBatchTestConfig{{TestName: "TestA", ITRSubtests: []processRetrySubtestITRConfig{{TestName: "TestB/child"}}}}},
		"duplicate ITR":        {Version: processRetryBatchVersion, Tests: []processRetryBatchTestConfig{{TestName: "TestA", ITRSubtests: []processRetrySubtestITRConfig{{TestName: "TestA/child"}, {TestName: "TestA/child"}}}}},
	}

	for name, cfg := range tests {
		t.Run(name, func(t *testing.T) {
			require.Error(t, validateProcessRetryBatchConfig(cfg))
		})
	}
}

func TestProcessRetryTestManagementSubtests(t *testing.T) {
	identity := newTestIdentity("module", "suite", "TestA")
	modules := &net.TestManagementTestsResponseDataModules{Modules: map[string]net.TestManagementTestsResponseDataSuites{
		"module": {Suites: map[string]net.TestManagementTestsResponseDataTests{
			"suite": {Tests: map[string]net.TestManagementTestsResponseDataTestProperties{
				"TestA/disabled": {Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{Disabled: true}},
				"TestA/enabled":  {},
				"TestA/atf":      {Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{Disabled: true, AttemptToFix: true}},
				"TestB/disabled": {Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{Disabled: true}},
			}},
		}},
	}}

	disabled, attemptToFix := processRetryTestManagementSubtests(*identity, modules)
	require.Equal(t, []string{"TestA/disabled"}, disabled)
	require.Equal(t, []string{"TestA/atf"}, attemptToFix)
}

func TestProcessRetryITRSubtests(t *testing.T) {
	identity := newTestIdentity("module", "suite", "TestA")
	state := &itrState{
		settings: &net.SettingsResponseData{ItrEnabled: true, TestsSkipping: true},
		response: &net.SkippableTestsResponse{Skippables: map[string]map[string][]net.SkippableResponseDataAttributes{
			"suite": {
				"TestA/safe": {
					{Name: "TestA/safe"},
					{Name: "TestA/safe", MissingLineCodeCoverage: true},
				},
				"TestA/parameterized": {{Name: "TestA/parameterized", Parameters: `{"case":"one"}`}},
				"TestB/other":         {{Name: "TestB/other"}},
			},
		}},
	}
	require.Equal(t, []processRetrySubtestITRConfig{{
		TestName:                "TestA/safe",
		MissingLineCodeCoverage: true,
	}}, processRetryITRSubtests(*identity, state))

	state.settings.TestsSkipping = false
	require.Empty(t, processRetryITRSubtests(*identity, state))
}

func TestPreserveProcessRetryBatchFailure(t *testing.T) {
	a := deferredProcessRetryBatchGroup("TestA", 1)
	b := deferredProcessRetryBatchGroup("TestB", 2)
	processErr := errors.New("testmain teardown")
	completed := map[*deferredProcessRetryGroup]processRetryAttemptResult{
		a: deferredProcessRetryPassingAttempt(1),
		b: deferredProcessRetryPassingAttempt(2),
	}

	preserveProcessRetryBatchFailure(processRetryAttemptResult{ExitCode: 3, ExitStatusObserved: true, Err: processErr}, []*deferredProcessRetryGroup{a, b}, completed)

	require.Equal(t, "process_exit", effectiveProcessRetryStatus(completed[a], false).FailureKind)
	require.ErrorIs(t, completed[a].Err, processErr)
	require.ErrorIs(t, completed[a].Err, errProcessRetryBatchFailed)
	require.Equal(t, processRetryStatusPass, effectiveProcessRetryStatus(completed[b], false).Status)
}

func TestProcessRetryBatchFailureClassification(t *testing.T) {
	_, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	batchFailure := deferredProcessRetryPassingAttempt(1)
	batchFailure.ExitCode = 1
	batchFailure.Err = errProcessRetryBatchFailed
	testFailure := completedProcessRetryAttempt(processRetryResult{Status: processRetryStatusFail, Failed: true})
	testPanic := completedProcessRetryAttempt(processRetryResult{Status: processRetryStatusFail, Failed: true, Panic: true})
	testRace := completedProcessRetryAttempt(processRetryResult{Status: processRetryStatusFail, Failed: true, RaceDetected: true})
	tests := []struct {
		name              string
		attempt           processRetryAttemptResult
		wantPackageFailed bool
	}{
		{name: "unexplained batch exit", attempt: batchFailure, wantPackageFailed: true},
		{name: "timeout", attempt: processRetryAttemptResult{TimedOut: true}, wantPackageFailed: true},
		{name: "unreaped", attempt: processRetryAttemptResult{Unreaped: true}, wantPackageFailed: true},
		{name: "setup", attempt: processRetryAttemptResult{SetupFailure: true}, wantPackageFailed: true},
		{name: "missing result", attempt: processRetryAttemptResult{Err: errProcessRetryResultMissing}, wantPackageFailed: true},
		{name: "invalid result", attempt: processRetryAttemptResult{Err: errProcessRetryResultInvalid}, wantPackageFailed: true},
		{name: "launch error", attempt: processRetryAttemptResult{Err: errors.New("launch failed")}, wantPackageFailed: true},
		{name: "test failure", attempt: testFailure},
		{name: "test panic", attempt: testPanic},
		{name: "test race", attempt: testRace},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			coordinator := newProcessRetryCoordinatorForTesting(false)
			group := newDeferredQuarantinedFirstAttemptGroupForTesting("TestA", 1, 1)
			require.True(t, coordinator.beginAdmission().commit(group))
			coordinator.batchRunner = func(context.Context, []*deferredProcessRetryGroup) map[*deferredProcessRetryGroup]processRetryAttemptResult {
				return map[*deferredProcessRetryGroup]processRetryAttemptResult{group: tt.attempt}
			}

			summary := coordinator.drain(0)

			require.Equal(t, tt.wantPackageFailed, summary.packageFailed)
			require.Equal(t, tt.wantPackageFailed, summary.deferredFailed)
			if tt.wantPackageFailed {
				require.Equal(t, processRetryFailureExitCode, summary.exitCode)
			} else {
				require.Zero(t, summary.exitCode)
			}
		})
	}
}

func TestPreserveProcessRetryBatchFailureKeepsDecodedFailure(t *testing.T) {
	a := deferredProcessRetryBatchGroup("TestA", 1)
	b := deferredProcessRetryBatchGroup("TestB", 2)
	completed := map[*deferredProcessRetryGroup]processRetryAttemptResult{
		a: completedProcessRetryAttempt(processRetryResult{Status: processRetryStatusFail, Failed: true}),
		b: deferredProcessRetryPassingAttempt(2),
	}

	preserveProcessRetryBatchFailure(processRetryAttemptResult{ExitCode: 1, ExitStatusObserved: true}, []*deferredProcessRetryGroup{a, b}, completed)

	require.Equal(t, "test_fail", effectiveProcessRetryStatus(completed[a], false).FailureKind)
	require.Equal(t, processRetryStatusPass, effectiveProcessRetryStatus(completed[b], false).Status)
}

func TestPreserveProcessRetryBatchFailureKeepsTestLogMergeFailure(t *testing.T) {
	a := deferredProcessRetryBatchGroup("TestA", 1)
	completed := map[*deferredProcessRetryGroup]processRetryAttemptResult{
		a: completedProcessRetryAttempt(processRetryResult{Status: processRetryStatusFail, Failed: true}),
	}

	preserveProcessRetryBatchFailure(processRetryAttemptResult{
		Err: errors.Join(errProcessRetryTestLogMerge, errors.New("malformed child log")),
	}, []*deferredProcessRetryGroup{a}, completed)

	require.True(t, completed[a].SetupFailure)
	require.ErrorIs(t, completed[a].Err, errProcessRetryTestLogMerge)
}

func TestProcessRetryBatchConfigRejectsUnknownAndTrailingData(t *testing.T) {
	for name, payload := range map[string]string{
		"unknown field": `{"version":1,"tests":[{"test_name":"TestA"}],"unknown":true}`,
		"trailing data": `{"version":1,"tests":[{"test_name":"TestA"}]} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "batch.json")
			require.NoError(t, os.WriteFile(path, []byte(payload), processRetryBatchManifestMode))
			_, err := readProcessRetryBatchConfig(path)
			require.Error(t, err)
		})
	}
}

func TestProcessRetryBatchArgsPreserveSegmentedSelectors(t *testing.T) {
	snapshot := captureProcessRetryArgsSnapshot([]string{
		"-test.run=^TestQuarantined$/wanted$",
		"-test.skip=destructive$",
		"-test.testlogfile=parent-testlog.txt",
		"-test.gocoverdir=parent-coverage",
	})

	require.Equal(t, "parent-testlog.txt", snapshot.testLogFile)
	got, ok, reason := buildProcessRetryArgsFromSnapshot(processRetryBatchArgsSnapshot(snapshot, "child-testlog.txt", true), ".", 1, time.Second)

	require.True(t, ok, reason)
	require.Equal(t, []string{
		"-test.failfast=false",
		"-test.testlogfile=child-testlog.txt",
		"-test.gocoverdir=parent-coverage",
		"-test.run=^TestQuarantined$/wanted$",
		"-test.skip=destructive$",
		"-test.count=1",
		"-test.cpu=1",
		"-test.timeout=1s",
	}, got)
	require.NotContains(t, processRetryBatchArgsSnapshot(snapshot, "", false).preserved, "-test.gocoverdir=parent-coverage")
}

func TestProcessRetryBatchOutputOmitsChildRunnerProtocol(t *testing.T) {
	output := strings.Join([]string{
		"\x16=== RUN   TestSelected",
		"direct stdout",
		"    selected_test.go:10: test log",
		"PASS",
		"FAIL",
		"coverage: custom metric",
		"=== application state",
		"--- FAIL: application state",
		"\x16--- FAIL: TestSelected (0.01s)",
		"WARNING: DATA RACE",
		"direct stderr",
		"\x16FAIL",
	}, "\n") + "\n"

	require.Equal(t, strings.Join([]string{
		"direct stdout",
		"    selected_test.go:10: test log",
		"PASS",
		"FAIL",
		"coverage: custom metric",
		"=== application state",
		"--- FAIL: application state",
		"WARNING: DATA RACE",
		"direct stderr",
	}, "\n")+"\n", filterProcessRetryBatchOutput(output))
}

func TestMergeProcessRetryTestLog(t *testing.T) {
	parentPath := filepath.Join(t.TempDir(), "parent-testlog.txt")
	childPath := filepath.Join(t.TempDir(), "child-testlog.txt")
	require.NoError(t, os.WriteFile(parentPath, []byte(processRetryTestLogMagic+"getenv PARENT\n"), 0o600))
	require.NoError(t, os.WriteFile(childPath, []byte(processRetryTestLogMagic+"getenv CHILD\nopen /tmp/input\n"), 0o600))

	require.NoError(t, mergeProcessRetryTestLog(parentPath, childPath, "/workspace/package"))
	got, err := os.ReadFile(parentPath)
	require.NoError(t, err)
	require.Equal(t, processRetryTestLogMagic+"getenv PARENT\nchdir /workspace/package\ngetenv CHILD\nopen /tmp/input\n", string(got))

	require.NoError(t, os.WriteFile(childPath, []byte("getenv MISSING_HEADER\n"), 0o600))
	require.Error(t, mergeProcessRetryTestLog(parentPath, childPath, "/workspace/package"))
}

func TestNativeScheduledBatchMergesTestLogAfterParentFlush(t *testing.T) {
	dir := t.TempDir()
	parentPath := filepath.Join(dir, "parent-testlog.txt")
	childPath := filepath.Join(dir, "child-testlog.txt")
	require.NoError(t, os.WriteFile(parentPath, []byte(processRetryTestLogMagic), 0o600))
	require.NoError(t, os.WriteFile(childPath, []byte(processRetryTestLogMagic+"getenv CHILD\n"), 0o600))

	done := make(chan struct{})
	close(done)
	batch := &nativeScheduledProcessRetryBatch{
		batch:      &processRetryBatchConfig{},
		resultRoot: filepath.Join(dir, "result.json"),
		done:       done,
		cancel:     func() {},
		attempt: processRetryAttemptResult{testLogMerge: &processRetryTestLogMerge{
			parentPath:            parentPath,
			childPath:             childPath,
			childWorkingDirectory: "/workspace/package",
		}},
	}
	coordinator := newProcessRetryCoordinatorForTesting(false)
	coordinator.nativeBatches = map[uint64]*nativeScheduledProcessRetryBatch{1: batch}

	// Simulate testing.StopTestLog flushing its buffered parent records after
	// the native-scheduled child has already completed.
	require.NoError(t, os.WriteFile(parentPath, []byte(processRetryTestLogMagic+"getenv PARENT\n"), 0o600))
	attempt, ok := coordinator.nativeScheduledBatchResult(1)
	require.True(t, ok)
	require.NoError(t, attempt.Err)

	got, err := os.ReadFile(parentPath)
	require.NoError(t, err)
	require.Equal(t, processRetryTestLogMagic+"getenv PARENT\nchdir /workspace/package\ngetenv CHILD\n", string(got))
}

func TestRelaunchedNativeScheduledBatchMergesTestLog(t *testing.T) {
	requireProcessRetryContainmentForTesting(t)
	resetProcessRetryLimiterForTesting(t)
	dir := t.TempDir()
	parentPath := filepath.Join(dir, "parent-testlog.txt")
	counterPath := filepath.Join(dir, "cleanup-counter")
	require.NoError(t, os.WriteFile(parentPath, []byte(processRetryTestLogMagic), 0o600))
	t.Setenv(processRetryNativeLifecycleFixtureEnv, "true")
	t.Setenv(processRetryChildResultScenarioEnv, "cleanup_once")
	t.Setenv(processRetryChildCleanupCounterPathEnv, counterPath)

	baseline := captureProcessRetryLaunchBaselineForTesting()
	require.NoError(t, baseline.err)
	baseline.args = []string{"-test.testlogfile=" + parentPath}
	baseline.argsSnapshot = captureProcessRetryArgsSnapshot(baseline.args)
	group := deferredProcessRetryBatchGroup("TestProcessRetryChildResultFixture", 1)
	group.launchBaseline = baseline

	completed, missing := runDeferredNativeScheduledProcessRetryBatchOnce(context.Background(), []*deferredProcessRetryGroup{group})
	require.Empty(t, missing)
	require.NoError(t, completed[group].Err)
	got, err := os.ReadFile(parentPath)
	require.NoError(t, err)
	require.Contains(t, string(got), "chdir "+baseline.workingDirectory+"\n")
	require.Contains(t, string(got), "getenv "+processRetryChildResultScenarioEnv+"\n")
	require.Contains(t, string(got), "open "+counterPath+"\n")
}

func TestProcessRetryBatchTimeoutUsesPackageBudget(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	deadline := now.Add(30 * time.Minute)
	disabled := captureProcessRetryArgsSnapshot([]string{"-test.timeout=0"})

	require.True(t, disabled.timeoutSet)
	require.Zero(t, disabled.timeout)
	require.Zero(t, selectedProcessRetryTimeout(
		true, disabled.timeout, disabled.timeoutSet, 0, false, time.Time{}, false, now,
	))
	require.Equal(t, 5*time.Minute, selectedProcessRetryTimeout(
		true, disabled.timeout, disabled.timeoutSet, 5*time.Minute, true, time.Time{}, false, now,
	))
	require.Equal(t, 30*time.Minute-processRetryParentDeadlineReserve(), selectedProcessRetryTimeout(
		true, disabled.timeout, disabled.timeoutSet, 0, false, deadline, true, now,
	))
	require.Equal(t, 30*time.Minute-processRetryParentDeadlineReserve(), selectedProcessRetryTimeout(
		true, 30*time.Minute, true, 0, false, deadline, true, now,
	))
	require.Equal(t, 5*time.Minute, selectedProcessRetryTimeout(
		true, 30*time.Minute, true, 5*time.Minute, true, deadline, true, now,
	))
	args, ok, reason := buildProcessRetryArgsFromSnapshot(disabled, ".", 1, 0)
	require.True(t, ok, reason)
	require.Contains(t, args, "-test.timeout=0s")
}

func TestProcessRetryBatchCoverageFlushBeforeTerminalReplay(t *testing.T) {
	tests := []struct {
		name   string
		result retryAttemptResult
		want   bool
	}{
		{name: "pass"},
		{name: "ordinary failure", result: retryAttemptResult{failed: true}},
		{name: "native fatal", result: retryAttemptResult{nativeFatalTraceReplay: true}, want: true},
		{name: "panic", result: retryAttemptResult{panicData: "panic"}, want: true},
		{name: "cleanup panic", result: retryAttemptResult{cleanupPanicData: "panic"}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, requiresProcessRetryBatchCoverageFlush(tt.result))
		})
	}
}

func TestProcessRetryBatchRetriesOnlyMissingGroups(t *testing.T) {
	a := deferredProcessRetryBatchGroup("TestA", 1)
	b := deferredProcessRetryBatchGroup("TestB", 2)
	c := deferredProcessRetryBatchGroup("TestC", 3)
	var calls [][]*deferredProcessRetryGroup
	runOnce := func(_ context.Context, groups []*deferredProcessRetryGroup) (
		map[*deferredProcessRetryGroup]processRetryAttemptResult,
		map[*deferredProcessRetryGroup]processRetryAttemptResult,
	) {
		calls = append(calls, append([]*deferredProcessRetryGroup(nil), groups...))
		if len(calls) == 1 {
			return map[*deferredProcessRetryGroup]processRetryAttemptResult{a: deferredProcessRetryPassingAttempt(1)}, map[*deferredProcessRetryGroup]processRetryAttemptResult{
				b: {Err: errProcessRetryResultMissing},
				c: {Err: errProcessRetryResultMissing},
			}
		}
		return map[*deferredProcessRetryGroup]processRetryAttemptResult{
			b: deferredProcessRetryPassingAttempt(2),
			c: deferredProcessRetryPassingAttempt(3),
		}, nil
	}

	results := runDeferredQuarantinedProcessRetryBatchWithRunner(context.Background(), []*deferredProcessRetryGroup{a, b, c}, runOnce)

	require.Equal(t, [][]*deferredProcessRetryGroup{{a, b, c}, {b, c}}, calls)
	require.Equal(t, processRetryStatusPass, results[a].Result.Status)
	require.Equal(t, processRetryStatusPass, results[b].Result.Status)
	require.Equal(t, processRetryStatusPass, results[c].Result.Status)
}

func TestProcessRetryBatchNoProgressFallsBackToSingletons(t *testing.T) {
	a := deferredProcessRetryBatchGroup("TestA", 1)
	b := deferredProcessRetryBatchGroup("TestB", 2)
	var calls [][]*deferredProcessRetryGroup
	runOnce := func(_ context.Context, groups []*deferredProcessRetryGroup) (
		map[*deferredProcessRetryGroup]processRetryAttemptResult,
		map[*deferredProcessRetryGroup]processRetryAttemptResult,
	) {
		calls = append(calls, append([]*deferredProcessRetryGroup(nil), groups...))
		if len(groups) > 1 {
			return nil, map[*deferredProcessRetryGroup]processRetryAttemptResult{
				a: {Err: errProcessRetryResultMissing},
				b: {Err: errProcessRetryResultMissing},
			}
		}
		return map[*deferredProcessRetryGroup]processRetryAttemptResult{groups[0]: deferredProcessRetryPassingAttempt(len(calls))}, nil
	}

	results := runDeferredQuarantinedProcessRetryBatchWithRunner(context.Background(), []*deferredProcessRetryGroup{a, b}, runOnce)

	require.Equal(t, [][]*deferredProcessRetryGroup{{a, b}, {a}, {b}}, calls)
	require.Equal(t, processRetryStatusPass, results[a].Result.Status)
	require.Equal(t, processRetryStatusPass, results[b].Result.Status)
}

func TestProcessRetryBatchCancellationDoesNotSplitPendingWork(t *testing.T) {
	a := deferredProcessRetryBatchGroup("TestA", 1)
	b := deferredProcessRetryBatchGroup("TestB", 2)
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	runOnce := func(_ context.Context, _ []*deferredProcessRetryGroup) (
		map[*deferredProcessRetryGroup]processRetryAttemptResult,
		map[*deferredProcessRetryGroup]processRetryAttemptResult,
	) {
		calls++
		cancel()
		return nil, map[*deferredProcessRetryGroup]processRetryAttemptResult{
			a: {Err: context.Canceled},
			b: {Err: context.Canceled},
		}
	}

	results := runDeferredQuarantinedProcessRetryBatchWithRunner(ctx, []*deferredProcessRetryGroup{a, b}, runOnce)

	require.Equal(t, 1, calls)
	require.ErrorIs(t, results[a].Err, context.Canceled)
	require.ErrorIs(t, results[b].Err, context.Canceled)
}

func TestProcessRetryBatchGlobalStopDoesNotSplitPendingWork(t *testing.T) {
	a := deferredProcessRetryBatchGroup("TestA", 1)
	b := deferredProcessRetryBatchGroup("TestB", 2)
	calls := 0
	runOnce := func(_ context.Context, _ []*deferredProcessRetryGroup) (
		map[*deferredProcessRetryGroup]processRetryAttemptResult,
		map[*deferredProcessRetryGroup]processRetryAttemptResult,
	) {
		calls++
		attempt := processRetryAttemptResult{Unreaped: true, Err: errProcessRetryChildUnreaped}
		return nil, map[*deferredProcessRetryGroup]processRetryAttemptResult{a: attempt, b: attempt}
	}

	results := runDeferredQuarantinedProcessRetryBatchWithRunner(context.Background(), []*deferredProcessRetryGroup{a, b}, runOnce)

	require.Equal(t, 1, calls)
	require.True(t, results[a].Unreaped)
	require.True(t, results[b].Unreaped)
}

func TestNativeProcessRetryBatchRequiresOneTest(t *testing.T) {
	a := deferredProcessRetryBatchGroup("TestA", 1)
	b := deferredProcessRetryBatchGroup("TestB", 2)

	completed, missing := runDeferredProcessRetryBatchOnce(context.Background(), []*deferredProcessRetryGroup{a, b}, true)

	require.Empty(t, completed)
	require.Len(t, missing, 2)
	for _, group := range []*deferredProcessRetryGroup{a, b} {
		require.True(t, missing[group].SetupFailure)
		require.EqualError(t, missing[group].Err, "native process retry batch must contain one test")
	}
}

func TestCompletedProcessRetryAttemptPreservesPanicSemantics(t *testing.T) {
	attempt := completedProcessRetryAttempt(processRetryResult{
		Status: processRetryStatusControlledPanicReady,
		Failed: true,
		Panic:  true,
	})

	require.Equal(t, processRetryControlledPanicExitCode, attempt.ExitCode)
	require.True(t, attempt.ControlledTerminalCommitted)
	require.Equal(t, "test_panic", effectiveProcessRetryStatus(attempt, false).FailureKind)
}

func TestProcessRetryBatchKeepsOutputPerTest(t *testing.T) {
	a := completedProcessRetryAttempt(processRetryResult{
		Status:     processRetryStatusPass,
		OutputTail: "test-a-output-sentinel",
	})
	b := completedProcessRetryAttempt(processRetryResult{
		Status:          processRetryStatusPass,
		OutputTail:      "test-b-output-sentinel",
		OutputTruncated: true,
	})

	require.Equal(t, "test-a-output-sentinel", a.OutputTail)
	require.NotContains(t, a.OutputTail, "test-b-output-sentinel")
	require.Contains(t, b.OutputTail, processRetryOutputTruncationMarker)
	require.Contains(t, b.OutputTail, "test-b-output-sentinel")
	require.NotContains(t, b.OutputTail, "test-a-output-sentinel")
}

func deferredProcessRetryBatchGroup(name string, ordinal uint64) *deferredProcessRetryGroup {
	identity := newTestIdentity("module", "suite", name)
	return &deferredProcessRetryGroup{
		identity:          *identity,
		mRunEpoch:         1,
		phaseID:           1,
		invocationOrdinal: ordinal,
	}
}

func fixedProcessRetryAttempt(status processRetryStatus, timestamp int64) processRetryAttemptResult {
	start := time.Unix(0, timestamp)
	return processRetryAttemptResult{
		Result:             processRetryResult{Status: status},
		ExitCode:           0,
		ExitStatusObserved: true,
		StartTime:          start,
		FinishTime:         start.Add(time.Nanosecond),
	}
}

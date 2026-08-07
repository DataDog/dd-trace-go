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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
)

func TestProcessRetryBatchConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "batch.json")
	want := &processRetryBatchConfig{
		Version:                processRetryBatchVersion,
		CollectPerTestCoverage: true,
		Tests: []processRetryBatchTestConfig{
			{TestName: "TestA", InvocationOrdinal: 11, DisabledSubtests: []string{"TestA/disabled"}},
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
	require.Equal(t, []string{"TestA/disabled"}, child.batchTest.DisabledSubtests)
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
}

func TestProcessRetryBatchConfigRejectsInvalidManifests(t *testing.T) {
	validTest := processRetryBatchTestConfig{TestName: "TestA"}
	tests := map[string]*processRetryBatchConfig{
		"nil":                 nil,
		"wrong version":       {Version: processRetryBatchVersion + 1, Tests: []processRetryBatchTestConfig{validTest}},
		"empty":               {Version: processRetryBatchVersion},
		"missing name":        {Version: processRetryBatchVersion, Tests: []processRetryBatchTestConfig{{InvocationOrdinal: 1}}},
		"duplicate test name": {Version: processRetryBatchVersion, Tests: []processRetryBatchTestConfig{validTest, validTest}},
	}

	for name, cfg := range tests {
		t.Run(name, func(t *testing.T) {
			require.Error(t, validateProcessRetryBatchConfig(cfg))
		})
	}
}

func TestDisabledProcessRetrySubtests(t *testing.T) {
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

	require.Equal(t, []string{"TestA/disabled"}, disabledProcessRetrySubtests(*identity, modules))
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
	})

	require.Equal(t, "parent-testlog.txt", snapshot.testLogFile)
	got, ok, reason := buildProcessRetryArgsFromSnapshot(processRetryBatchArgsSnapshot(snapshot, "child-testlog.txt"), ".", 1, time.Second)

	require.True(t, ok, reason)
	require.Equal(t, []string{
		"-test.failfast=false",
		"-test.testlogfile=child-testlog.txt",
		"-test.run=^TestQuarantined$/wanted$",
		"-test.skip=destructive$",
		"-test.count=1",
		"-test.cpu=1",
		"-test.timeout=1s",
	}, got)
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

func TestProcessRetryBatchTimeoutUsesPackageBudget(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	deadline := now.Add(30 * time.Minute)

	require.Equal(t, 30*time.Minute-processRetryParentDeadlineReserve(), selectedProcessRetryTimeout(
		true, 30*time.Minute, true, 0, false, deadline, true, now,
	))
	require.Equal(t, 5*time.Minute, selectedProcessRetryTimeout(
		true, 30*time.Minute, true, 5*time.Minute, true, deadline, true, now,
	))
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

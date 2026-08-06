// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProcessRetryBatchConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "batch.json")
	want := &processRetryBatchConfig{
		Version:                processRetryBatchVersion,
		CollectPerTestCoverage: true,
		Tests: []processRetryBatchTestConfig{
			{TestName: "TestA", InvocationOrdinal: 11},
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
	}, 1, got.Tests[1])
	require.Equal(t, "TestB", child.TestName)
	require.Equal(t, 1, child.Attempt)
	require.Equal(t, processRetryBatchReason, child.RetryReason)
	require.Equal(t, uint64(7), child.MRunEpoch)
	require.Equal(t, uint64(12), child.InvocationOrdinal)
	require.True(t, child.CollectPerTestCoverage)
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
	})

	got, ok, reason := buildProcessRetryArgsFromSnapshot(processRetryBatchArgsSnapshot(snapshot), ".", 1, time.Second)

	require.True(t, ok, reason)
	require.Equal(t, []string{
		"-test.failfast=false",
		"-test.run=^TestQuarantined$/wanted$",
		"-test.skip=destructive$",
		"-test.count=1",
		"-test.cpu=1",
		"-test.timeout=1s",
	}, got)
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

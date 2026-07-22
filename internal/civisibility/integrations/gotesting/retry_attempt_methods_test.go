// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProcessRetryParityFreshRunnerStablePublicState(t *testing.T) {
	originalDeadline, originalDeadlineOK := t.Deadline()
	var observedName string
	var observedDeadlineOK bool
	var observedDeadlineEqual bool
	var observedContextDistinct bool
	var observedInitiallyPassed bool
	attempt, result, reason := runFreshRetryAttempt(t, func(local *testing.T) {
		observedName = local.Name()
		deadline, ok := local.Deadline()
		observedDeadlineOK = ok
		observedDeadlineEqual = deadline == originalDeadline
		observedContextDistinct = t.Context() != local.Context()
		observedInitiallyPassed = !local.Failed() && !local.Skipped()
	})
	require.Empty(t, reason)
	require.NotNil(t, attempt)
	defer attempt.group.retire()
	require.False(t, result.failed)
	require.Equal(t, t.Name(), observedName)
	require.Equal(t, originalDeadlineOK, observedDeadlineOK)
	require.True(t, observedDeadlineEqual)
	require.True(t, observedContextDistinct)
	require.True(t, observedInitiallyPassed)
}

func TestProcessRetryParityFreshRunnerRestoresSetenvAndChdir(t *testing.T) {
	const key = "DD_RETRY_ATTEMPT_PUBLIC_METHOD"
	originalValue, originalSet := os.LookupEnv(key)
	originalDir, err := os.Getwd()
	require.NoError(t, err)
	targetDir := t.TempDir()
	var setenvObserved bool
	var chdirObserved string

	attempt, result, reason := runFreshRetryAttempt(t, func(local *testing.T) {
		local.Setenv(key, "attempt-value")
		setenvObserved = os.Getenv(key) == "attempt-value"
		local.Chdir(targetDir)
		chdirObserved, err = os.Getwd()
		if err != nil {
			local.Error(err)
		}
	})
	require.Empty(t, reason)
	require.NotNil(t, attempt)
	defer attempt.group.retire()
	require.False(t, result.failed)
	require.True(t, setenvObserved)
	require.Equal(t, targetDir, chdirObserved)

	value, set := os.LookupEnv(key)
	require.Equal(t, originalSet, set)
	if originalSet {
		require.Equal(t, originalValue, value)
	}
	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.Equal(t, originalDir, cwd)
}

func TestProcessRetryParityFreshRunnerAttrValidationAndCapture(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		value      string
		wantFailed bool
	}{
		{name: "valid", key: "component", value: "retry-attempt"},
		{name: "invalid key", key: "invalid key", value: "value", wantFailed: true},
		{name: "invalid value", key: "key", value: "invalid\nvalue", wantFailed: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			attempt, result, reason := runFreshRetryAttempt(t, func(local *testing.T) {
				local.Attr(tc.key, tc.value)
			})
			require.Empty(t, reason)
			require.NotNil(t, attempt)
			defer attempt.group.retire()
			require.Equal(t, tc.wantFailed, result.failed)
			if tc.wantFailed {
				require.NotEmpty(t, result.output)
			} else if pointerWord(commonBaseForTest(attempt.test, attempt.layout), attempt.layout.common.chatty) != nil {
				require.Contains(t, string(result.output), "=== ATTR")
				require.Contains(t, string(result.output), tc.key+" "+tc.value)
			}
		})
	}
}

func TestProcessRetryParityFreshRunnerConcurrentReportingMethods(t *testing.T) {
	attempt, result, reason := runFreshRetryAttempt(t, func(local *testing.T) {
		writer := local.Output()
		var workers sync.WaitGroup
		for i := range 16 {
			workers.Go(func() {
				local.Helper()
				local.Logf("worker %d", i)
				if _, err := writer.Write([]byte("output\n")); err != nil {
					local.Errorf("Output.Write: %v", err)
				}
			})
		}
		workers.Wait()
	})
	require.Empty(t, reason)
	require.NotNil(t, attempt)
	defer attempt.group.retire()
	require.False(t, result.failed)
	require.Contains(t, string(result.output), "worker")
	require.Contains(t, string(result.output), "output")
}

func TestProcessRetryParityFreshRunnerRunDuringCleanupPanics(t *testing.T) {
	attempt, result, reason := runFreshRetryAttempt(t, func(local *testing.T) {
		local.Cleanup(func() {
			local.Run("forbidden", func(*testing.T) {})
		})
	})
	require.Empty(t, reason)
	require.NotNil(t, attempt)
	defer attempt.group.retire()
	require.True(t, result.failed)
	require.Equal(t, retryAttemptCompletionPanic, result.completionPhase)
	require.Equal(t, "testing: t.Run called during t.Cleanup", result.cleanupPanicData)
}

func TestProcessRetryParityFreshRunnerTempDirIsAttemptLocal(t *testing.T) {
	var firstPaths []string
	first, firstResult, reason := runFreshRetryAttempt(t, func(local *testing.T) {
		firstPaths = append(firstPaths, local.TempDir(), local.TempDir())
		if err := os.WriteFile(filepath.Join(firstPaths[0], "sentinel"), []byte("first"), 0o600); err != nil {
			local.Error(err)
		}
	})
	require.Empty(t, reason)
	require.NotNil(t, first)
	defer first.group.retire()
	require.False(t, firstResult.failed)
	require.Len(t, firstPaths, 2)
	require.NotEqual(t, firstPaths[0], firstPaths[1])
	for _, path := range firstPaths {
		_, err := os.Stat(path)
		require.ErrorIs(t, err, os.ErrNotExist)
	}

	var secondPath string
	second, secondResult, reason := runFreshRetryAttempt(t, func(local *testing.T) {
		secondPath = local.TempDir()
	})
	require.Empty(t, reason)
	require.NotNil(t, second)
	defer second.group.retire()
	require.False(t, secondResult.failed)
	require.NotContains(t, firstPaths, secondPath)
}

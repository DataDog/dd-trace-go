//go:build go1.26 && !go1.27

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"os"
	"path/filepath"
	"testing"
	"testing/cryptotest"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProcessRetryParitySynctestUsesOneNativeExecution(t *testing.T) {
	var bodyCalls, retryCallbacks int
	synctest.Test(t, func(bubble *testing.T) {
		runTestWithRetry(&runTestWithRetryOptions{
			t: bubble,
			targetFunc: func(local *testing.T) {
				bodyCalls++
				require.Same(t, bubble, local)
			},
			postAdjustRetryCount: func(*testExecutionMetadata, time.Duration) int64 {
				retryCallbacks++
				return 1
			},
		})
	})
	require.Equal(t, 1, bodyCalls)
	require.Zero(t, retryCallbacks)
}

func TestProcessRetryParityFreshRunnerCryptotestPreservesParallelRestrictions(t *testing.T) {
	t.Run("global random then Parallel", func(t *testing.T) {
		attempt, result, reason := runFreshRetryAttempt(t, func(local *testing.T) {
			cryptotest.SetGlobalRandom(local, 1)
			local.Parallel()
		})
		require.Empty(t, reason)
		require.NotNil(t, attempt)
		defer attempt.group.retire()
		require.True(t, result.failed)
		require.Equal(t, retryAttemptCompletionPanic, result.completionPhase)
		requireRetryAttemptParallelConflict(t, result.panicData)
	})

	t.Run("Parallel ancestor then global random", func(t *testing.T) {
		group, reason := newRetryAttemptGroup(t)
		require.Empty(t, reason)
		defer group.retire()

		first, firstResult, reason := runFreshRetryAttemptInGroup(group, func(local *testing.T) {
			local.Parallel()
		})
		require.Empty(t, reason)
		require.NotNil(t, first)
		require.False(t, firstResult.failed)

		second, secondResult, reason := runFreshRetryAttemptInGroup(group, func(local *testing.T) {
			cryptotest.SetGlobalRandom(local, 2)
		})
		require.Empty(t, reason)
		require.NotNil(t, second)
		require.True(t, secondResult.failed)
		require.Equal(t, retryAttemptCompletionPanic, secondResult.completionPhase)
		requireRetryAttemptParallelConflict(t, secondResult.panicData)
	})
}

func TestProcessRetryParityFreshRunnerArtifactDirIsAttemptLocal(t *testing.T) {
	var firstPaths []string
	first, firstResult, reason := runFreshRetryAttempt(t, func(local *testing.T) {
		firstPaths = append(firstPaths, local.ArtifactDir(), local.ArtifactDir())
		if err := os.WriteFile(filepath.Join(firstPaths[0], "sentinel"), []byte("first"), 0o600); err != nil {
			local.Error(err)
		}
	})
	require.Empty(t, reason)
	require.NotNil(t, first)
	defer first.group.retire()
	require.False(t, firstResult.failed)
	require.Len(t, firstPaths, 2)
	require.Equal(t, firstPaths[0], firstPaths[1])
	_, err := os.Stat(firstPaths[0])
	require.ErrorIs(t, err, os.ErrNotExist)

	var secondPath string
	second, secondResult, reason := runFreshRetryAttempt(t, func(local *testing.T) {
		secondPath = local.ArtifactDir()
	})
	require.Empty(t, reason)
	require.NotNil(t, second)
	defer second.group.retire()
	require.False(t, secondResult.failed)
	require.NotEqual(t, firstPaths[0], secondPath)
}

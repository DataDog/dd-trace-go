// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"context"
	"os"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/locking"
	"github.com/stretchr/testify/require"
)

type retryParityTrace struct {
	mu     locking.Mutex
	events []string
}

func (t *retryParityTrace) add(event string) {
	t.mu.Lock()
	t.events = append(t.events, event)
	t.mu.Unlock()
}

func (t *retryParityTrace) snapshot() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.events...)
}

func TestProcessRetryParityDifferentialNativeAndFreshLifecycle(t *testing.T) {
	tests := []struct {
		name    string
		target  func(*testing.T, *retryParityTrace, *string)
		skipped bool
	}{
		{
			name: "normal methods and cleanups",
			target: func(local *testing.T, trace *retryParityTrace, tempDir *string) {
				trace.add("body:start")
				local.Log("log line")
				local.Logf("formatted %s", "log line")
				_, err := local.Output().Write([]byte("partial output"))
				require.NoError(t, err)
				require.NoError(t, local.Context().Err())
				_, deadlinePresent := local.Deadline()
				trace.add("deadline:" + boolString(deadlinePresent))
				*tempDir = local.TempDir()
				_, err = os.Stat(*tempDir)
				require.NoError(t, err)
				local.Helper()
				local.Cleanup(func() {
					trace.add("cleanup:oldest:context:" + boolString(local.Context().Err() == context.Canceled))
				})
				local.Cleanup(func() { trace.add("cleanup:newest") })
				trace.add("body:end")
			},
		},
		{
			name:    "skip and cleanup",
			skipped: true,
			target: func(local *testing.T, trace *retryParityTrace, _ *string) {
				local.Cleanup(func() {
					trace.add("cleanup:context:" + boolString(local.Context().Err() == context.Canceled))
				})
				trace.add("body:skip")
				local.SkipNow()
			},
		},
		{
			name: "parallel child",
			target: func(local *testing.T, trace *retryParityTrace, _ *string) {
				trace.add("body:start")
				local.Run("child", func(child *testing.T) {
					trace.add("child:before-parallel")
					child.Parallel()
					trace.add("child:after-parallel")
					child.Cleanup(func() { trace.add("child:cleanup") })
				})
				trace.add("body:after-run")
				local.Cleanup(func() { trace.add("root:cleanup") })
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var nativeTrace retryParityTrace
			var nativeTempDir string
			var nativeTest *testing.T
			nativeRunResult := t.Run("native", func(native *testing.T) {
				nativeTest = native
				tc.target(native, &nativeTrace, &nativeTempDir)
			})
			require.True(t, nativeRunResult)

			var freshTrace retryParityTrace
			var freshTempDir string
			attempt, freshResult, reason := runFreshRetryAttempt(t, func(fresh *testing.T) {
				tc.target(fresh, &freshTrace, &freshTempDir)
			})
			require.Empty(t, reason)
			require.NotNil(t, attempt)
			defer attempt.cancelContexts()

			require.Equal(t, nativeTrace.snapshot(), freshTrace.snapshot())
			require.Equal(t, nativeTest.Failed(), freshResult.failed)
			require.Equal(t, nativeTest.Skipped(), freshResult.skipped)
			require.Equal(t, tc.skipped, freshResult.skipped)
			nativeFields := getTestPrivateFields(nativeTest)
			require.NotNil(t, nativeFields)
			require.Equal(t, *nativeFields.finished, freshResult.finished)
			require.Equal(t, string(*nativeFields.output), string(freshResult.nativeOutput))
			layout, layoutReason := getRetryAttemptLayout()
			require.Empty(t, layoutReason)
			nativeBase := commonBaseForTest(nativeTest, layout)
			require.Equal(t, *fieldPtr[bool](nativeBase, layout.common.done), freshResult.done)
			require.Equal(t, *fieldPtr[bool](nativeBase, layout.common.ran), freshResult.ran)
			if nativeTempDir != "" {
				_, nativeErr := os.Stat(nativeTempDir)
				_, freshErr := os.Stat(freshTempDir)
				require.True(t, os.IsNotExist(nativeErr))
				require.True(t, os.IsNotExist(freshErr))
			}
		})
	}
}

func TestProcessRetryParityDifferentialRootParallelScheduling(t *testing.T) {
	var nativeTrace retryParityTrace
	t.Run("native-container", func(container *testing.T) {
		container.Run("target", func(target *testing.T) {
			nativeTrace.add("body:before-parallel")
			target.Parallel()
			nativeTrace.add("body:after-parallel")
		})
		nativeTrace.add("parent:after-run")
	})

	var freshTrace retryParityTrace
	t.Run("fresh-container", func(container *testing.T) {
		container.Run("target", func(original *testing.T) {
			attempt, result, reason := runFreshRetryAttempt(original, func(target *testing.T) {
				freshTrace.add("body:before-parallel")
				target.Parallel()
				freshTrace.add("body:after-parallel")
			})
			require.Empty(original, reason)
			require.NotNil(original, attempt)
			defer attempt.cancelContexts()
			require.False(original, result.failed)
		})
		freshTrace.add("parent:after-run")
	})

	require.Equal(t, nativeTrace.snapshot(), freshTrace.snapshot())
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

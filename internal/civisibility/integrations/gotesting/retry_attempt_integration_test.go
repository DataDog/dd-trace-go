// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func retryParityOrchestrionCleanupFailure(t *testing.T) {
	t.Log("retry parity fresh attempt log")
	t.Cleanup(t.Fail)
}

func TestProcessRetryParityFreshRuntimeOwnsCallbacksAndMetadata(t *testing.T) {
	createTestMetadata(t, nil)
	defer deleteTestMetadata(t)
	metadataBefore := retryParityMetadataCount()
	var (
		bodyCalls         int
		perExecutionCalls int
		seenContexts      []any
		lastAttempt       *testing.T
	)

	runTestWithRetry(&runTestWithRetryOptions{
		t: t,
		targetFunc: func(local *testing.T) {
			bodyCalls++
			seenContexts = append(seenContexts, local.Context())
			if bodyCalls == 1 {
				local.Cleanup(local.Fail)
			}
		},
		preExecMetaAdjust: func(execMeta *testExecutionMetadata, executionIndex int) {
			require.Equal(t, executionIndex > 0, execMeta.isARetry)
		},
		preIsLastRetry: func(_ *testExecutionMetadata, _ int, remainingRetries int64) bool {
			return remainingRetries == 1
		},
		postAdjustRetryCount: func(_ *testExecutionMetadata, _ time.Duration) int64 {
			return 1
		},
		postPerExecution: func(local *testing.T, _ *testExecutionMetadata, executionIndex int, _ time.Duration) {
			perExecutionCalls++
			if executionIndex == 0 {
				require.True(t, local.Failed(), "cleanup failure must be complete before retry policy")
			}
			lastAttempt = local
		},
		postShouldRetry: func(local *testing.T, _ *testExecutionMetadata, executionIndex int, _ int64) bool {
			return executionIndex == 0 && local.Failed()
		},
		postOnRetryEnd: func(_ *testing.T, executionIndex int, local *testing.T, _ retryGroupPolicyResult) {
			require.Equal(t, 1, executionIndex)
			require.Same(t, lastAttempt, local)
			require.False(t, local.Failed())
			require.Equal(t, metadataBefore+2, retryParityMetadataCount(), "attempt metadata must remain bound through aggregate callbacks")
		},
	})

	require.Equal(t, 2, bodyCalls)
	require.Equal(t, 2, perExecutionCalls)
	require.Len(t, seenContexts, 2)
	require.NotSame(t, seenContexts[0], seenContexts[1])
	require.Equal(t, metadataBefore, retryParityMetadataCount(), "group retirement must release all metadata tombstones")
	require.False(t, t.Failed())
}

func TestProcessRetryParityUnsupportedFreshLayoutUsesOneNativeParentExecution(t *testing.T) {
	var (
		bodyCalls       int
		callbackCalls   int
		factoryReceives *testing.T
	)

	runTestWithRetry(&runTestWithRetryOptions{
		t: t,
		retryAttemptGroupFactory: func(original *testing.T) (*retryAttemptGroup, string) {
			factoryReceives = original
			return nil, "unsupported_test_layout"
		},
		targetFunc: func(local *testing.T) {
			bodyCalls++
			require.Same(t, t, local)
			execMeta := getTestMetadata(local)
			require.NotNil(t, execMeta)
			require.True(t, execMeta.suppressCoverageCollection)
			require.False(t, shouldCollectExecutionCoverage(true, execMeta))
		},
		postAdjustRetryCount: func(*testExecutionMetadata, time.Duration) int64 {
			callbackCalls++
			return 3
		},
		postShouldRetry: func(*testing.T, *testExecutionMetadata, int, int64) bool {
			callbackCalls++
			return true
		},
		postOnRetryEnd: func(*testing.T, int, *testing.T, retryGroupPolicyResult) {
			callbackCalls++
		},
	})

	require.Same(t, t, factoryReceives)
	require.Equal(t, 1, bodyCalls)
	require.Zero(t, callbackCalls)
}

func TestProcessRetryParityFirstAttemptCreationFailureUsesNativeFallback(t *testing.T) {
	group, reason := newRetryAttemptGroup(t)
	require.Empty(t, reason)
	group.retire()

	bodyCalls := 0
	runTestWithRetry(&runTestWithRetryOptions{
		t: t,
		retryAttemptGroupFactory: func(*testing.T) (*retryAttemptGroup, string) {
			return group, ""
		},
		targetFunc: func(local *testing.T) {
			bodyCalls++
			require.Same(t, t, local)
		},
	})
	require.Equal(t, 1, bodyCalls)
}

func TestProcessRetryParityMaskedFallbackRunsInstrumentedShellWithoutUserBody(t *testing.T) {
	var shellCalls, bodyCalls, maskingFinalizers int
	runTestWithRetry(&runTestWithRetryOptions{
		t:                           t,
		processRetryIdentity:        newTestIdentity("module", "suite", "TestParent/selected"),
		retryAttemptMaskingFallback: true,
		targetFunc: func(local *testing.T) {
			shellCalls++
			execMeta := getTestMetadata(local)
			require.NotNil(t, execMeta)
			if !execMeta.suppressUserTestBody {
				bodyCalls++
			}
		},
		postOnRetryEnd: func(*testing.T, int, *testing.T, retryGroupPolicyResult) {
			maskingFinalizers++
		},
	})
	require.Equal(t, 1, shellCalls)
	require.Zero(t, bodyCalls)
	require.Equal(t, 1, maskingFinalizers)
}

func TestProcessRetryParitySelectedSubtestUsesOneNativeExecutionWithoutFreshLayout(t *testing.T) {
	var bodyCalls, metadataCalls int
	require.True(t, t.Run("selected", func(subtest *testing.T) {
		runTestWithRetry(&runTestWithRetryOptions{
			t:                    subtest,
			processRetryIdentity: newTestIdentity("module", "suite", "TestParent/selected"),
			preExecMetaAdjust: func(execMeta *testExecutionMetadata, executionIndex int) {
				metadataCalls++
				require.Zero(t, executionIndex)
				execMeta.isAttemptToFix = true
			},
			targetFunc: func(local *testing.T) {
				bodyCalls++
				require.Same(t, subtest, local)
				require.True(t, getTestMetadata(local).isAttemptToFix)
			},
			postAdjustRetryCount: func(*testExecutionMetadata, time.Duration) int64 { return 3 },
			postShouldRetry: func(*testing.T, *testExecutionMetadata, int, int64) bool {
				return true
			},
		})
	}))
	require.Equal(t, 1, bodyCalls)
	require.Equal(t, 1, metadataCalls)
}

func TestProcessRetryParityCoverageCollectorBelongsOnlyToParentAttempt(t *testing.T) {
	require.False(t, shouldCollectExecutionCoverage(false, nil))
	require.True(t, shouldCollectExecutionCoverage(true, nil))
	require.True(t, shouldCollectExecutionCoverage(true, &testExecutionMetadata{}))
	require.False(t, shouldCollectExecutionCoverage(true, &testExecutionMetadata{isARetry: true}))
	require.False(t, shouldCollectExecutionCoverage(true, &testExecutionMetadata{suppressCoverageCollection: true}))
}

func TestProcessRetryParityDetectedRaceStopsEveryRetryBackend(t *testing.T) {
	cancelCalls := 0
	execOpts := &executionOptions{
		retryCount:               3,
		processRetryPolicyCancel: func() { cancelCalls++ },
	}

	require.True(t, stopRetryGroupAfterRaceLocked(execOpts, true))
	require.Zero(t, execOpts.retryCount)
	require.True(t, execOpts.rawAttemptFailureSeen)
	require.Equal(t, 1, cancelCalls)

	require.False(t, stopRetryGroupAfterRaceLocked(execOpts, false))
	require.Equal(t, 1, cancelCalls)
}

func TestProcessRetryParityFailfastStopsAfterFirstRawFailure(t *testing.T) {
	createTestMetadata(t, nil)
	defer deleteTestMetadata(t)
	var bodyCalls, retryPolicyCalls int
	runTestWithRetry(&runTestWithRetryOptions{
		t:                      t,
		failfastEnabled:        func() bool { return true },
		nativeFailfastObserved: func() bool { return false },
		targetFunc: func(local *testing.T) {
			bodyCalls++
			local.Fail()
		},
		postAdjustRetryCount: func(*testExecutionMetadata, time.Duration) int64 { return 2 },
		postShouldRetry: func(*testing.T, *testExecutionMetadata, int, int64) bool {
			retryPolicyCalls++
			return true
		},
	})

	require.Equal(t, 1, bodyCalls, "the admitted parent attempt must run exactly once")
	require.Zero(t, retryPolicyCalls, "failfast must stop before family retry policy and reservation")
}

func TestProcessRetryParityExternalFailfastDoesNotSuppressFirstBody(t *testing.T) {
	createTestMetadata(t, nil)
	defer deleteTestMetadata(t)
	var bodyCalls, retryPolicyCalls int
	runTestWithRetry(&runTestWithRetryOptions{
		t:                      t,
		failfastEnabled:        func() bool { return true },
		nativeFailfastObserved: func() bool { return true },
		targetFunc: func(*testing.T) {
			bodyCalls++
		},
		postAdjustRetryCount: func(*testExecutionMetadata, time.Duration) int64 { return 2 },
		postShouldRetry: func(*testing.T, *testExecutionMetadata, int, int64) bool {
			retryPolicyCalls++
			return true
		},
	})

	require.Equal(t, 1, bodyCalls, "native runTests already admitted the first parent body")
	require.Zero(t, retryPolicyCalls, "external failfast must stop only additional work")
}

func TestProcessRetryParityFailfastUsesLiveValueAtContinuation(t *testing.T) {
	t.Run("false_to_true", func(t *testing.T) {
		createTestMetadata(t, nil)
		defer deleteTestMetadata(t)
		var enabled atomic.Bool
		var bodyCalls int
		runTestWithRetry(&runTestWithRetryOptions{
			t:                      t,
			failfastEnabled:        enabled.Load,
			nativeFailfastObserved: func() bool { return false },
			targetFunc: func(local *testing.T) {
				bodyCalls++
				local.Fail()
			},
			postAdjustRetryCount: func(*testExecutionMetadata, time.Duration) int64 { return 1 },
			postPerExecution: func(*testing.T, *testExecutionMetadata, int, time.Duration) {
				enabled.Store(true)
			},
			postShouldRetry: func(*testing.T, *testExecutionMetadata, int, int64) bool { return true },
		})
		require.Equal(t, 1, bodyCalls)
	})

	t.Run("true_to_false", func(t *testing.T) {
		createTestMetadata(t, nil)
		defer deleteTestMetadata(t)
		var enabled atomic.Bool
		enabled.Store(true)
		var bodyCalls int
		runTestWithRetry(&runTestWithRetryOptions{
			t:                      t,
			failfastEnabled:        enabled.Load,
			nativeFailfastObserved: func() bool { return false },
			targetFunc: func(local *testing.T) {
				bodyCalls++
				if bodyCalls == 1 {
					local.Fail()
				}
			},
			postAdjustRetryCount: func(*testExecutionMetadata, time.Duration) int64 { return 1 },
			preIsLastRetry:       func(*testExecutionMetadata, int, int64) bool { return true },
			postPerExecution: func(_ *testing.T, _ *testExecutionMetadata, executionIndex int, _ time.Duration) {
				if executionIndex == 0 {
					enabled.Store(false)
				}
			},
			postShouldRetry: func(_ *testing.T, _ *testExecutionMetadata, executionIndex int, _ int64) bool {
				return executionIndex == 0
			},
		})
		require.Equal(t, 2, bodyCalls, "the initial live value must not be frozen")
	})
}

func TestProcessRetryParityOrchestrionFinalizesAfterFreshCleanup(t *testing.T) {
	recorder, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	oldEnabled := atomic.LoadInt32(&ciVisibilityEnabledValue)
	atomic.StoreInt32(&ciVisibilityEnabledValue, 1)
	defer atomic.StoreInt32(&ciVisibilityEnabledValue, oldEnabled)

	createTestMetadata(t, nil)
	defer deleteTestMetadata(t)
	wrapper := instrumentTestingTFunc(retryParityOrchestrionCleanupFailure)
	runTestWithRetry(&runTestWithRetryOptions{
		t:          t,
		targetFunc: wrapper,
		postAdjustRetryCount: func(*testExecutionMetadata, time.Duration) int64 {
			return 0
		},
		postShouldRetry: func(*testing.T, *testExecutionMetadata, int, int64) bool {
			return false
		},
	})

	require.False(t, t.Failed(), "the fresh cleanup failure must not leak into the original test")
	require.Len(t, recorder.tests, 1)
	require.Equal(t, processRetryStatusFail, recorder.tests[0].status)
	require.Equal(t, 1, recorder.tests[0].closeCount)
	require.Equal(t, true, recorder.tests[0].tags["error"])
	require.Contains(t, strings.Join(recorder.tests[0].logs, "\n"), "retry parity fresh attempt log")
}

func TestProcessRetryParityWrapperPreservesNativeFatalPanic(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessRetryParityWrapperNativeFatalFixture$", "-test.count=1", "-test.timeout=10s")
	cmd.Env = append(os.Environ(), "Bypass=true", "RETRY_PARITY_WRAPPER_NATIVE_FATAL_FIXTURE=true")
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	require.Error(t, err)
	require.Contains(t, output.String(), "retry parity wrapper native fatal")
	require.NotContains(t, output.String(), "test failed and panicked after")
	require.NotContains(t, output.String(), "test timed out")
}

func TestProcessRetryParityNestedOrchestrionPanicRemainsNativeFatal(t *testing.T) {
	markerPath := filepath.Join(t.TempDir(), "continued")
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessRetryParityNestedOrchestrionPanicFixture$", "-test.count=1", "-test.timeout=10s")
	cmd.Env = append(os.Environ(),
		"Bypass=true",
		"RETRY_PARITY_NESTED_ORCHESTRION_PANIC_FIXTURE=true",
		"RETRY_PARITY_NESTED_ORCHESTRION_PANIC_MARKER="+markerPath,
	)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	require.Error(t, err)
	require.Contains(t, output.String(), "retry parity nested orchestrion panic")
	_, statErr := os.Stat(markerPath)
	require.ErrorIs(t, statErr, os.ErrNotExist, "native subtest panic must not return to the parent body")
}

func TestProcessRetryParityNestedOrchestrionPanicFixture(t *testing.T) {
	if os.Getenv("RETRY_PARITY_NESTED_ORCHESTRION_PANIC_FIXTURE") != "true" {
		t.Skip("subprocess fixture")
	}
	setRetryParityRecordingRuntime(t)
	nested := instrumentTestingTFunc(func(*testing.T) {
		panic("retry parity nested orchestrion panic")
	})
	runTestWithRetry(&runTestWithRetryOptions{
		t: t,
		targetFunc: func(local *testing.T) {
			local.Run("nested", nested)
			if err := os.WriteFile(os.Getenv("RETRY_PARITY_NESTED_ORCHESTRION_PANIC_MARKER"), []byte("continued"), 0o600); err != nil {
				local.Fatal(err)
			}
		},
		postAdjustRetryCount: func(*testExecutionMetadata, time.Duration) int64 { return 0 },
		postShouldRetry:      func(*testing.T, *testExecutionMetadata, int, int64) bool { return false },
	})
}

func TestProcessRetryParityNestedOrchestrionCleanupPanicRemainsNativeFatal(t *testing.T) {
	markerPath := filepath.Join(t.TempDir(), "continued")
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessRetryParityNestedOrchestrionCleanupPanicFixture$", "-test.count=1", "-test.timeout=10s")
	cmd.Env = append(os.Environ(),
		"Bypass=true",
		"RETRY_PARITY_NESTED_ORCHESTRION_CLEANUP_PANIC_FIXTURE=true",
		"RETRY_PARITY_NESTED_ORCHESTRION_CLEANUP_PANIC_MARKER="+markerPath,
	)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	require.Error(t, err)
	require.Contains(t, output.String(), "retry parity nested orchestrion cleanup panic")
	_, statErr := os.Stat(markerPath)
	require.ErrorIs(t, statErr, os.ErrNotExist, "native cleanup panic must not return to the parent body")
}

func TestProcessRetryParityNestedOrchestrionCleanupPanicFixture(t *testing.T) {
	if os.Getenv("RETRY_PARITY_NESTED_ORCHESTRION_CLEANUP_PANIC_FIXTURE") != "true" {
		t.Skip("subprocess fixture")
	}
	setRetryParityRecordingRuntime(t)
	nested := instrumentTestingTFunc(func(t *testing.T) {
		t.Cleanup(func() { panic("retry parity nested orchestrion cleanup panic") })
	})
	runTestWithRetry(&runTestWithRetryOptions{
		t: t,
		targetFunc: func(local *testing.T) {
			local.Run("nested", nested)
			if err := os.WriteFile(os.Getenv("RETRY_PARITY_NESTED_ORCHESTRION_CLEANUP_PANIC_MARKER"), []byte("continued"), 0o600); err != nil {
				local.Fatal(err)
			}
		},
		postAdjustRetryCount: func(*testExecutionMetadata, time.Duration) int64 { return 0 },
		postShouldRetry:      func(*testing.T, *testExecutionMetadata, int, int64) bool { return false },
	})
}

func TestProcessRetryParityNestedOrchestrionBareCleanupGoexitDoesNotFail(t *testing.T) {
	markerPath := filepath.Join(t.TempDir(), "passed")
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessRetryParityNestedOrchestrionBareCleanupGoexitFixture$", "-test.count=1", "-test.timeout=10s")
	cmd.Env = append(os.Environ(),
		"Bypass=true",
		"RETRY_PARITY_NESTED_ORCHESTRION_CLEANUP_GOEXIT_FIXTURE=true",
		"RETRY_PARITY_NESTED_ORCHESTRION_CLEANUP_GOEXIT_MARKER="+markerPath,
	)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))
	marker, err := os.ReadFile(markerPath)
	require.NoError(t, err)
	require.Equal(t, "passed", string(marker))
}

func TestProcessRetryParityNestedOrchestrionBareCleanupGoexitFixture(t *testing.T) {
	if os.Getenv("RETRY_PARITY_NESTED_ORCHESTRION_CLEANUP_GOEXIT_FIXTURE") != "true" {
		t.Skip("subprocess fixture")
	}
	setRetryParityRecordingRuntime(t)
	nested := instrumentTestingTFunc(func(t *testing.T) {
		t.Cleanup(runtime.Goexit)
	})
	runTestWithRetry(&runTestWithRetryOptions{
		t: t,
		targetFunc: func(local *testing.T) {
			if !local.Run("nested", nested) {
				return
			}
			if err := os.WriteFile(os.Getenv("RETRY_PARITY_NESTED_ORCHESTRION_CLEANUP_GOEXIT_MARKER"), []byte("passed"), 0o600); err != nil {
				local.Fatal(err)
			}
		},
		postAdjustRetryCount: func(*testExecutionMetadata, time.Duration) int64 { return 0 },
		postShouldRetry:      func(*testing.T, *testExecutionMetadata, int, int64) bool { return false },
	})
}

func setRetryParityRecordingRuntime(t *testing.T) {
	_, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	t.Cleanup(restoreSession)
	oldEnabled := atomic.LoadInt32(&ciVisibilityEnabledValue)
	atomic.StoreInt32(&ciVisibilityEnabledValue, 1)
	t.Cleanup(func() { atomic.StoreInt32(&ciVisibilityEnabledValue, oldEnabled) })
	createTestMetadata(t, nil)
	t.Cleanup(func() { deleteTestMetadata(t) })
}

func TestProcessRetryParityWrapperNativeFatalFixture(t *testing.T) {
	if os.Getenv("RETRY_PARITY_WRAPPER_NATIVE_FATAL_FIXTURE") != "true" {
		t.Skip("subprocess fixture")
	}
	createTestMetadata(t, nil)
	defer deleteTestMetadata(t)
	runTestWithRetry(&runTestWithRetryOptions{
		t: t,
		targetFunc: func(local *testing.T) {
			local.Run("parallel", func(child *testing.T) { child.Parallel() })
			panic("retry parity wrapper native fatal")
		},
		postAdjustRetryCount: func(*testExecutionMetadata, time.Duration) int64 { return 0 },
		postShouldRetry:      func(*testing.T, *testExecutionMetadata, int, int64) bool { return false },
		postOnRetryEnd: func(original *testing.T, _ int, local *testing.T, _ retryGroupPolicyResult) {
			if local.Failed() {
				original.Fail()
			}
		},
	})
}

func retryParityMetadataCount() int {
	ciVisibilityTestMetadataMutex.RLock()
	defer ciVisibilityTestMetadataMutex.RUnlock()
	return len(ciVisibilityTestMetadata)
}

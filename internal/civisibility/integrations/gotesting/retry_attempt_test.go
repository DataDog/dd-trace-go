// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"context"
	"io"
	"reflect"
	"sync"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
)

func retryParityOriginalTestA(*testing.T) {}
func retryParityOriginalTestB(*testing.T) {}
func retryParityWrappedTestA(*testing.T)  {}
func retryParityWrappedTestB(*testing.T)  {}
func retryParityUnownedTest(*testing.T)   {}

func retryParityOriginalBenchmark(*testing.B) {}
func retryParityWrappedBenchmark(*testing.B)  {}
func retryParityUnownedBenchmark(*testing.B)  {}

func TestProcessRetryParityRuntimeLayoutIsCachedAndAvailable(t *testing.T) {
	layout := getTestingInternalsLayout()
	require.Same(t, layout, getTestingInternalsLayout())
	retryLayout, reason := getRetryAttemptLayout()
	require.Empty(t, reason)
	require.Same(t, layout, retryLayout)
}

func TestProcessRetryParityRestoresOwnedWorkloadsInCurrentOrder(t *testing.T) {
	tests := []testing.InternalTest{
		{Name: "B", F: retryParityWrappedTestB},
		{Name: "unowned", F: retryParityUnownedTest},
		{Name: "A", F: retryParityWrappedTestA},
	}
	benchmarks := []testing.InternalBenchmark{
		{Name: "unowned", F: retryParityUnownedBenchmark},
		{Name: "owned", F: retryParityWrappedBenchmark},
	}

	restoreTestingMTests(&tests, map[string]func(*testing.T){
		"A": retryParityOriginalTestA,
		"B": retryParityOriginalTestB,
	})
	restoreTestingMBenchmarks(&benchmarks, map[string]func(*testing.B){
		"owned": retryParityOriginalBenchmark,
	})

	require.Equal(t, []string{"B", "unowned", "A"}, []string{tests[0].Name, tests[1].Name, tests[2].Name})
	require.Equal(t, reflect.ValueOf(retryParityOriginalTestB).Pointer(), reflect.ValueOf(tests[0].F).Pointer())
	require.Equal(t, reflect.ValueOf(retryParityUnownedTest).Pointer(), reflect.ValueOf(tests[1].F).Pointer())
	require.Equal(t, reflect.ValueOf(retryParityOriginalTestA).Pointer(), reflect.ValueOf(tests[2].F).Pointer())
	require.Equal(t, []string{"unowned", "owned"}, []string{benchmarks[0].Name, benchmarks[1].Name})
	require.Equal(t, reflect.ValueOf(retryParityUnownedBenchmark).Pointer(), reflect.ValueOf(benchmarks[0].F).Pointer())
	require.Equal(t, reflect.ValueOf(retryParityOriginalBenchmark).Pointer(), reflect.ValueOf(benchmarks[1].F).Pointer())
}

func TestProcessRetryParityTestingMClaimLinearizesConcurrentAndSequentialCalls(t *testing.T) {
	m := &testing.M{}
	claim, disposition := claimTestingMInstrumentation(m)
	require.Equal(t, testingMClaimOwner, disposition)
	require.NotNil(t, claim)

	conflictingClaim, disposition := claimTestingMInstrumentation(m)
	require.Equal(t, testingMClaimActiveConflict, disposition)
	require.Same(t, claim, conflictingClaim)

	retireTestingMInstrumentation(m, claim)
	retiredClaim, disposition := claimTestingMInstrumentation(m)
	require.Equal(t, testingMClaimRetiredNative, disposition)
	require.Same(t, claim, retiredClaim)
}

func TestProcessRetryParityRuntimeLayoutRejectsMissingCapabilities(t *testing.T) {
	layout, reason := validateRetryAttemptLayout(nil)
	require.Nil(t, layout)
	require.Equal(t, "testing_t_layout_unsupported", reason)

	unsupported := *getTestingInternalsLayout()
	unsupported.retryAttemptOK = false
	layout, reason = validateRetryAttemptLayout(&unsupported)
	require.Nil(t, layout)
	require.Equal(t, "testing_t_layout_unsupported", reason)
}

func TestProcessRetryParityNativeFailureObservationUsesPrivateRootState(t *testing.T) {
	attempt, reason := newRetryAttemptRoot(t)
	require.Empty(t, reason)
	require.NotNil(t, attempt)
	defer attempt.group.retire()

	require.False(t, retryAttemptNativeFailureObserved(attempt.test))
	parent := getTestParentPrivateFields(attempt.test)
	require.NotNil(t, parent)
	parent.SetFailed(true)
	require.True(t, retryAttemptNativeFailureObserved(attempt.test))
}

func TestProcessRetryParityGroupAdmissionRejectsUnsupportedDynamicModes(t *testing.T) {
	layout, reason := getRetryAttemptLayout()
	require.Empty(t, reason)
	base := commonBaseForTest(t, layout)
	mu := fieldPtr[sync.RWMutex](base, layout.common.mu)

	mu.Lock()
	originalFuzz := *fieldPtr[bool](base, layout.common.inFuzzFn)
	*fieldPtr[bool](base, layout.common.inFuzzFn) = true
	mu.Unlock()
	group, reason := newRetryAttemptGroup(t)
	mu.Lock()
	*fieldPtr[bool](base, layout.common.inFuzzFn) = originalFuzz
	mu.Unlock()
	require.Nil(t, group)
	require.Equal(t, "fuzz_active", reason)

	if !layout.common.isSynctest.available {
		return
	}
	mu.Lock()
	originalSynctest := *fieldPtr[bool](base, layout.common.isSynctest)
	*fieldPtr[bool](base, layout.common.isSynctest) = true
	mu.Unlock()
	group, reason = newRetryAttemptGroup(t)
	mu.Lock()
	*fieldPtr[bool](base, layout.common.isSynctest) = originalSynctest
	mu.Unlock()
	require.Nil(t, group)
	require.Equal(t, "synctest_unsupported", reason)
}

func TestProcessRetryParityFreshAttemptHasNoMutableAliases(t *testing.T) {
	first, reason := newRetryAttemptRoot(t)
	require.Empty(t, reason)
	require.NotNil(t, first)
	defer first.cancelContexts()

	second, reason := newRetryAttemptRoot(t)
	require.Empty(t, reason)
	require.NotNil(t, second)
	defer second.cancelContexts()

	layout, reason := getRetryAttemptLayout()
	require.Empty(t, reason)
	require.NotNil(t, layout)
	originalBase := commonBaseForTest(t, layout)
	firstBase := commonBaseForTest(first.test, layout)
	secondBase := commonBaseForTest(second.test, layout)
	require.NotNil(t, originalBase)
	require.NotNil(t, firstBase)
	require.NotNil(t, secondBase)

	require.Equal(t, t.Name(), first.test.Name())
	require.Equal(t, t.Name(), second.test.Name())
	require.NotSame(t, t.Context(), first.test.Context())
	require.NotSame(t, first.test.Context(), second.test.Context())
	require.Equal(t, getTestState(t), getTestState(first.test))
	require.Equal(t, getTestState(t), getTestState(second.test))

	require.NotEqual(t, *fieldPtr[chan bool](originalBase, layout.common.barrier), *fieldPtr[chan bool](firstBase, layout.common.barrier))
	require.NotEqual(t, *fieldPtr[chan bool](firstBase, layout.common.barrier), *fieldPtr[chan bool](secondBase, layout.common.barrier))
	require.NotEqual(t, *fieldPtr[chan bool](originalBase, layout.common.signal), *fieldPtr[chan bool](firstBase, layout.common.signal))
	require.NotEqual(t, *fieldPtr[chan bool](firstBase, layout.common.signal), *fieldPtr[chan bool](secondBase, layout.common.signal))
	require.NotEqual(t, pointerWord(originalBase, layout.common.parent), pointerWord(firstBase, layout.common.parent))
	require.Equal(t, unsafe.Pointer(commonBaseForTest(first.parent, layout)), pointerWord(firstBase, layout.common.parent))
	outputWriterField := wordCopiedField{unsafeField: layout.common.o}
	require.NotEqual(t, pointerWord(originalBase, outputWriterField), pointerWord(firstBase, outputWriterField))
	require.NotEqual(t, *fieldPtr[io.Writer](originalBase, layout.common.w), *fieldPtr[io.Writer](firstBase, layout.common.w))
	require.NotEqual(t, *fieldPtr[io.Writer](firstBase, layout.common.w), *fieldPtr[io.Writer](secondBase, layout.common.w))
	if originalChatty := pointerWord(originalBase, layout.common.chatty); originalChatty != nil {
		require.NotEqual(t, originalChatty, pointerWord(firstBase, layout.common.chatty))
		require.NotEqual(t, pointerWord(firstBase, layout.common.chatty), pointerWord(secondBase, layout.common.chatty))
	}

	originalOutput := append([]byte(nil), (*fieldPtr[[]byte](originalBase, layout.common.output))...)
	originalCleanups := len(*fieldPtr[[]func()](originalBase, layout.common.cleanups))
	originalHelpers := len(*fieldPtr[map[uintptr]struct{}](originalBase, layout.common.helperPCs))
	originalTempDir := *fieldPtr[string](originalBase, layout.common.tempDir)
	const attemptOnlyHelperPC = uintptr(1)
	(*fieldPtr[map[uintptr]struct{}](firstBase, layout.common.helperPCs))[attemptOnlyHelperPC] = struct{}{}
	_, originalHasAttemptHelper := (*fieldPtr[map[uintptr]struct{}](originalBase, layout.common.helperPCs))[attemptOnlyHelperPC]
	_, secondHasAttemptHelper := (*fieldPtr[map[uintptr]struct{}](secondBase, layout.common.helperPCs))[attemptOnlyHelperPC]

	first.test.Helper()
	first.test.Log("fresh attempt output")
	first.test.Cleanup(func() {})
	first.test.Fail()
	originalHelpersAfterAttempt := len(*fieldPtr[map[uintptr]struct{}](originalBase, layout.common.helperPCs))

	require.True(t, first.test.Failed())
	require.False(t, second.test.Failed())
	require.False(t, t.Failed())
	require.False(t, originalHasAttemptHelper)
	require.False(t, secondHasAttemptHelper)
	require.Greater(t, len(*fieldPtr[map[uintptr]struct{}](firstBase, layout.common.helperPCs)), 0)
	require.Equal(t, originalHelpers, originalHelpersAfterAttempt)
	require.Equal(t, originalCleanups, len(*fieldPtr[[]func()](originalBase, layout.common.cleanups)))
	require.Equal(t, originalOutput, *fieldPtr[[]byte](originalBase, layout.common.output))
	require.Empty(t, *fieldPtr[[]byte](secondBase, layout.common.output))
	require.Equal(t, originalTempDir, *fieldPtr[string](originalBase, layout.common.tempDir))
	require.Empty(t, *fieldPtr[string](firstBase, layout.common.tempDir))
	require.Empty(t, *fieldPtr[string](secondBase, layout.common.tempDir))

	cleanupResult := &testCleanupResult{}
	runTestCleanup(first.test, cleanupResult)
	require.True(t, cleanupResult.ran)
}

func TestProcessRetryParityFreshAttemptSnapshotsHelpersUnderNativeLock(t *testing.T) {
	started := make(chan struct{})
	stop := make(chan struct{})
	var worker sync.WaitGroup
	worker.Add(1)
	go func() {
		defer worker.Done()
		close(started)
		for {
			select {
			case <-stop:
				return
			default:
				t.Helper()
			}
		}
	}()
	<-started

	for range 100 {
		attempt, reason := newRetryAttemptRoot(t)
		require.Empty(t, reason)
		require.NotNil(t, attempt)
		attempt.cancelContexts()
	}
	close(stop)
	worker.Wait()
}

func TestProcessRetryParityFreshAttemptContextCancellationIsLocal(t *testing.T) {
	attempt, reason := newRetryAttemptRoot(t)
	require.Empty(t, reason)
	require.NotNil(t, attempt)

	originalContext := t.Context()
	attemptContext := attempt.test.Context()
	require.NoError(t, originalContext.Err())
	require.NoError(t, attemptContext.Err())

	attempt.cancelContexts()
	require.ErrorIs(t, attemptContext.Err(), context.Canceled)
	require.NoError(t, originalContext.Err())
}

func TestProcessRetryParityFreshStateCancelsReplacedContext(t *testing.T) {
	layout, reason := getRetryAttemptLayout()
	require.Empty(t, reason)

	local := createNewTestFast(layout)
	base := commonBaseForTest(local, layout)
	require.NotNil(t, base)
	replaced := *fieldPtr[context.Context](base, layout.common.ctx)
	require.NoError(t, replaced.Err())

	initializeRetryAttemptFreshState(base, layout, retryAttemptRaceErrors())
	require.ErrorIs(t, replaced.Err(), context.Canceled)
	if cancel := *fieldPtr[context.CancelFunc](base, layout.common.cancelCtx); cancel != nil {
		cancel()
	}
}

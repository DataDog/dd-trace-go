// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

func retryContinuationStopped(execOpts *executionOptions) bool {
	if execOpts == nil || execOpts.mutex == nil {
		return false
	}
	execOpts.mutex.Lock()
	defer execOpts.mutex.Unlock()
	return retryContinuationStoppedLocked(execOpts, nil, nil)
}

func retryContinuationStoppedLocked(execOpts *executionOptions, completed *testing.T, execMeta *testExecutionMetadata) bool {
	if execOpts == nil || execOpts.options == nil {
		return false
	}
	if (completed != nil && completed.Failed()) ||
		(execMeta != nil && execMeta.panicData != nil) ||
		(execOpts.retryAttemptGroup != nil && execOpts.retryAttemptGroup.hasLateFailure()) {
		execOpts.rawAttemptFailureSeen = true
	}
	failfastEnabled := execOpts.options.failfastEnabled
	if failfastEnabled == nil {
		failfastEnabled = retryAttemptFailfastEnabled
	}
	if !failfastEnabled() {
		return false
	}
	if execOpts.rawAttemptFailureSeen {
		execOpts.failfastRawFailure = true
		execOpts.retryCount = 0
		if execOpts.processRetryPolicyCancel != nil {
			execOpts.processRetryPolicyCancel()
		}
		return true
	}
	nativeFailfastObserved := execOpts.options.nativeFailfastObserved
	if nativeFailfastObserved == nil {
		nativeFailfastObserved = func() bool {
			return retryAttemptNativeFailureObserved(execOpts.options.t)
		}
	}
	if nativeFailfastObserved() {
		execOpts.nativeFailfastStop = true
		execOpts.retryCount = 0
		if execOpts.processRetryPolicyCancel != nil {
			execOpts.processRetryPolicyCancel()
		}
		return true
	}
	return false
}

// stopRetryGroupAfterRaceLocked applies Go's terminal race semantics to every
// retry backend. The caller must hold execOpts.mutex.
func stopRetryGroupAfterRaceLocked(execOpts *executionOptions, raceDetected bool) bool {
	if execOpts == nil || !raceDetected {
		return false
	}
	execOpts.rawAttemptFailureSeen = true
	execOpts.retryCount = 0
	if execOpts.processRetryPolicyCancel != nil {
		execOpts.processRetryPolicyCancel()
	}
	return true
}

// executeFreshRetryAttemptIteration adapts the fresh testing.T runtime to the
// existing retry callbacks for supported top-level test layouts.
func executeFreshRetryAttemptIteration(execOpts *executionOptions) bool {
	var (
		currentIndex int
		execMeta     *testExecutionMetadata
		shouldRetry  bool
	)

	prepare := func(attempt *retryAttemptRoot) string {
		execOpts.mutex.Lock()
		defer execOpts.mutex.Unlock()

		execOpts.executionIndex++
		currentIndex = execOpts.executionIndex
		if currentIndex > 0 {
			consumeFlakyRetryBudgetReservation(execOpts)
		}

		execMeta = createTestMetadata(attempt.test, nil)
		attempt.metadata = execMeta
		execMeta.flakyRetryBudgetReservation = execOpts.flakyRetryBudgetReservation
		execMeta.hasAdditionalFeatureWrapper = true
		execMeta.usesFreshRetryAttemptRuntime = true
		propagateTestExecutionMetadataFlags(execMeta, execOpts.originalExecutionMetadata)
		execMeta.isARetry = currentIndex > 0
		if execOpts.options.preExecMetaAdjust != nil {
			execOpts.options.preExecMetaAdjust(execMeta, currentIndex)
		}
		if execMeta.isARetry {
			execMeta.isLastRetry = execOpts.options.preIsLastRetry(execMeta, currentIndex, execOpts.retryCount)
		}
		execMeta.remainingRetries = execOpts.retryCount
		execMeta.isEfdInParallel = execOpts.effectiveParallelEFDActive && usesEfdRetrySemantics(execMeta)
		return ""
	}

	complete := func(attempt *retryAttemptRoot, result retryAttemptResult) {
		execOpts.mutex.Lock()
		defer execOpts.mutex.Unlock()

		localT := attempt.test
		if finalize := execMeta.retryAttemptFinalizer; finalize != nil {
			execMeta.retryAttemptFinalizer = nil
			finalize(result)
		}
		if execOpts.originalExecutionMetadata != nil {
			execOpts.originalExecutionMetadata.test = execMeta.test
		}
		if execMeta.test == nil && execMeta.identity != nil {
			log.Debug("execMeta.test nil for %s", execMeta.identity.FullName)
		}
		if execMeta.test != nil {
			currentSuite := execMeta.test.Suite()
			if execOpts.suite == nil && currentSuite != nil {
				execOpts.suite = currentSuite
			}
			if execOpts.module == nil && currentSuite != nil && currentSuite.Module() != nil {
				execOpts.module = currentSuite.Module()
			}
		}

		if result.panicData != nil {
			localT.Fail()
			if execMeta.panicData == nil {
				execMeta.panicData = result.panicData
				execMeta.panicStacktrace = string(result.panicStack)
			}
		}
		if execMeta.panicData != nil && execOpts.panicExecutionMetadata == nil {
			execOpts.panicExecutionMetadata = execMeta
		}

		if execOpts.options.postAdjustRetryCount != nil && currentIndex == 0 {
			execOpts.retryCount = execOpts.options.postAdjustRetryCount(execMeta, result.duration)
		}
		execOpts.retryCount--
		if execOpts.options.postPerExecution != nil {
			execOpts.options.postPerExecution(localT, execMeta, currentIndex, result.duration)
		}
		execOpts.ptrToLocalT = localT
		execOpts.executionMetadata = execMeta
		if result.nativeFatalRequired {
			execOpts.nativeFatalTrace = cloneRetryAttemptTerminalTrace(result.terminalTrace)
			execOpts.nativeFatalTraceReplay = result.nativeFatalTraceReplay
			if result.panicData != nil {
				execOpts.nativeFatalPanic = result.panicData
			} else if result.cleanupPanicData != nil {
				execOpts.nativeFatalPanic = result.cleanupPanicData
			}
			execOpts.retryCount = 0
			shouldRetry = false
			return
		}
		if stopRetryGroupAfterRaceLocked(execOpts, result.raceDetected) {
			shouldRetry = false
			return
		}
		shouldRetry = reserveRetryBudgetIfNeeded(execOpts, localT, execMeta, currentIndex)
	}

	_, _, reason := runFreshRetryAttemptInGroupWithCallbacks(
		execOpts.retryAttemptGroup,
		prepare,
		execOpts.options.targetFunc,
		complete,
	)
	if reason != "" {
		log.Debug("runTestWithRetry: fresh retry attempt creation stopped: %s", reason)
		execOpts.retryCount = 0
		if execOpts.executionIndex < 0 {
			runRetryAttemptCapabilityFallback(execOpts.options, reason)
			execOpts.capabilityFallbackCompleted = true
		}
		return false
	}
	return shouldRetry
}

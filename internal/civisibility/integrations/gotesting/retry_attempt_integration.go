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
	return retryContinuationStoppedWithDeferredAdmission(execOpts, nil, nil, false)
}

func retryContinuationStoppedAfterAttempt(execOpts *executionOptions, completed *testing.T, execMeta *testExecutionMetadata) bool {
	return retryContinuationStoppedWithDeferredAdmission(execOpts, completed, execMeta, false)
}

func retryContinuationStoppedForDeferredAdmission(execOpts *executionOptions, completed *testing.T, execMeta *testExecutionMetadata) bool {
	return retryContinuationStoppedWithDeferredAdmission(execOpts, completed, execMeta, true)
}

func retryContinuationStoppedWithDeferredAdmission(execOpts *executionOptions, completed *testing.T, execMeta *testExecutionMetadata, allowDeferredFirstFailure bool) bool {
	if execOpts == nil || execOpts.options == nil {
		return false
	}
	failfastEnabled := execOpts.options.failfastEnabled
	if failfastEnabled == nil {
		failfastEnabled = retryAttemptFailfastEnabled
	}
	if !failfastEnabled() {
		return false
	}
	rawFailureObserved := (completed != nil && completed.Failed()) ||
		(execMeta != nil && execMeta.panicData != nil) ||
		(execOpts.retryAttemptGroup != nil && execOpts.retryAttemptGroup.hasLateFailure())
	deferredFirstAttempt := allowDeferredFirstFailure && execOpts.executionIndex == 0 &&
		execOpts.options.processRetryCoordinator != nil
	if rawFailureObserved && !deferredFirstAttempt {
		execOpts.rawAttemptFailureSeen = true
	}
	if execOpts.rawAttemptFailureSeen {
		execOpts.failfastRawFailure = true
		execOpts.retryCount = 0
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
		return true
	}
	return false
}

// stopRetryGroupAfterRace applies Go's terminal race semantics to a fresh
// in-process retry group.
func stopRetryGroupAfterRace(execOpts *executionOptions, raceDetected bool) bool {
	if execOpts == nil || !raceDetected {
		return false
	}
	execOpts.rawAttemptFailureSeen = true
	execOpts.retryCount = 0
	return true
}

func logFreshRetryAttemptState(stage string, t *testing.T, result retryAttemptResult) {
	if !log.DebugEnabled() {
		return
	}
	name := "<nil>"
	failed := false
	skipped := false
	if t != nil {
		name = t.Name()
		failed = t.Failed()
		skipped = t.Skipped()
	}
	log.Debug(
		"gotesting: fresh retry attempt state stage=%s test=%q result_failed=%t result_skipped=%t test_failed=%t test_skipped=%t cleanup_observation=%d completion_phase=%d",
		stage,
		name,
		result.failed,
		result.skipped,
		failed,
		skipped,
		result.cleanupObservation,
		result.completionPhase,
	)
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
		execMeta.isEfdInParallel = execOpts.efdBatchMetadataActive && usesEfdRetrySemantics(execMeta)
		return ""
	}

	complete := func(attempt *retryAttemptRoot, result retryAttemptResult) {
		localT := attempt.test
		observation := observeFreshRetryAttempt(currentIndex, result)
		observation.rootParallel = attempt.group.rootParallelWasObserved()
		execOpts.lastObservation = observation
		logFreshRetryAttemptState("complete", localT, result)
		if finalize := execMeta.retryAttemptFinalizer; finalize != nil {
			defer func() {
				execMeta.retryAttemptFinalizer = nil
				defer completeDeferredProcessRetryEvent(execMeta)
				finalize(result)
			}()
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

		if observation.panicData != nil {
			localT.Fail()
			if execMeta.panicData == nil {
				execMeta.panicData = observation.panicData
				execMeta.panicStacktrace = string(observation.panicStack)
			}
		}
		if execMeta.panicData != nil && execOpts.panicExecutionMetadata == nil {
			execOpts.panicExecutionMetadata = execMeta
		}

		if execOpts.options.postAdjustRetryCount != nil && currentIndex == 0 {
			execOpts.retryCount = execOpts.options.postAdjustRetryCount(execMeta, observation.duration)
		}
		execOpts.retryCount--
		if execOpts.options.postPerExecution != nil {
			execOpts.options.postPerExecution(localT, execMeta, currentIndex, observation.duration)
		}
		execOpts.ptrToLocalT = localT
		execOpts.executionMetadata = execMeta
		if observation.nativeFatalRequired {
			execOpts.nativeFatalTrace = observation.terminalTrace
			execOpts.nativeFatalTraceReplay = observation.nativeFatalTraceReplay
			if observation.panicData != nil {
				execOpts.nativeFatalPanic = observation.panicData
			} else if observation.cleanupPanicData != nil {
				execOpts.nativeFatalPanic = observation.cleanupPanicData
			}
			execOpts.retryCount = 0
			shouldRetry = false
			execMeta.retryContinuationDecided = true
			execMeta.retryContinuationAdmitted = false
			return
		}
		if stopRetryGroupAfterRace(execOpts, observation.raceDetected) {
			shouldRetry = false
			execMeta.retryContinuationDecided = true
			execMeta.retryContinuationAdmitted = false
			return
		}
		shouldRetry = reserveRetryBudgetIfNeeded(execOpts, localT, execMeta, currentIndex)
		execMeta.retryContinuationDecided = true
		execMeta.retryContinuationAdmitted = shouldRetry
		if shouldRetry && currentIndex == 0 && !isProcessRetryChild() {
			execOpts.deferredQueued = enqueueDeferredProcessRetryGroup(execOpts)
			if execOpts.deferredQueued && deferredProcessRetryFirstFailureIsIrreversible(execMeta, observation) {
				execOpts.options.t.Fail()
			} else if !execOpts.deferredQueued && retryContinuationStoppedAfterAttempt(execOpts, localT, execMeta) {
				shouldRetry = false
				execMeta.retryContinuationAdmitted = false
			}
		}
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

func deferredProcessRetryFirstFailureIsIrreversible(meta *testExecutionMetadata, observation retryAttemptObservation) bool {
	return meta != nil && observation.failed && meta.isAttemptToFix && !meta.isDisabled && !meta.isQuarantined
}

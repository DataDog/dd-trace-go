// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gotesting

import (
	"sync/atomic"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
	civisibilitynet "github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
)

type retryOutcomeAccumulator struct {
	passed  int
	skipped int
	failed  int
}

func (o *retryOutcomeAccumulator) observe(failed, skipped bool) {
	switch {
	case failed:
		o.failed++
	case skipped:
		o.skipped++
	default:
		o.passed++
	}
}

func (o retryOutcomeAccumulator) anyPassed() bool {
	return o.passed > 0
}

func (o retryOutcomeAccumulator) anyFailed() bool {
	return o.failed > 0
}

func (o retryOutcomeAccumulator) allAttemptsPassed() bool {
	return o.failed == 0 && o.skipped == 0
}

func (o retryOutcomeAccumulator) allRetriesFailed() bool {
	return o.passed == 0 && o.skipped == 0
}

// calculateFinalStatus computes the test.final_status value based on the overall test outcome.
// Priority order: quarantined/disabled -> ATF fail (any fail) -> pass (any pass wins) -> fail -> skip -> fail.
func calculateFinalStatus(anyPassed, anyFailed, currentIsSkip, isQuarantined, isDisabled, isAttemptToFix bool) string {
	if isQuarantined || isDisabled {
		return constants.TestStatusSkip
	}
	// For ATF tests, if any execution failed, the test should be marked as failed
	// (flaky test that's still flaky in attempt-to-fix should fail the build)
	if isAttemptToFix && anyFailed {
		return constants.TestStatusFail
	}
	if anyPassed {
		return constants.TestStatusPass
	}
	if anyFailed {
		return constants.TestStatusFail
	}
	if currentIsSkip {
		return constants.TestStatusSkip
	}
	return constants.TestStatusFail
}

// computeAdjustedRetryCount selects and caches the retry count from the initial
// execution so span finalization and retry scheduling use the same duration bucket.
func computeAdjustedRetryCount(execMeta *testExecutionMetadata, duration time.Duration) int64 {
	if execMeta.initialRetryCountSet {
		return execMeta.initialRetryCount
	}
	retryCount := adjustedRetryCount(execMeta, duration)
	execMeta.initialRetryCount = retryCount
	execMeta.initialRetryCountSet = true
	return retryCount
}

func adjustedRetryCount(execMeta *testExecutionMetadata, duration time.Duration) int64 {
	settings := integrations.GetSettings()

	// Attempt To Fix retries are always set to the configured value.
	if execMeta.isAttemptToFix && execMeta.shouldOrchestrateAttemptToFix {
		return int64(settings.TestManagement.AttemptToFixRetries)
	}

	// Early Flake Detection adjusts the retry count based on test duration.
	if usesEfdRetrySemantics(execMeta) {
		if retryCount, ok := efdRetryCountForDuration(settings, duration); ok {
			return retryCount
		}
		if execMeta.isFlakyTestRetriesEnabled {
			execMeta.efdFellBackToFlakyRetries = true
		}
	}

	// Automatic flaky tests retries are set to the configured value.
	if execMeta.isFlakyTestRetriesEnabled {
		return flakyRetryCountForDuration(duration)
	}

	// No retries
	return 0
}

func efdRetryCountForDuration(settings *civisibilitynet.SettingsResponseData, duration time.Duration) (int64, bool) {
	slowTestRetries := settings.EarlyFlakeDetection.SlowTestRetries
	retryCounts := [...]int{slowTestRetries.FiveS, slowTestRetries.TenS, slowTestRetries.ThirtyS, slowTestRetries.FiveM}
	if bucket := retryDurationBucket(duration); bucket < len(retryCounts) {
		return int64(retryCounts[bucket]), true
	}
	return 0, false
}

// flakyRetryCountForDuration returns the automatic test retries count for a test whose first execution took
// duration. Dynamic retry counts replace the flat DD_CIVISIBILITY_FLAKY_RETRY_COUNT value when configured.
func flakyRetryCountForDuration(duration time.Duration) int64 {
	flakyRetries := integrations.GetFlakyRetriesSettings()
	if counts := flakyRetries.DynamicRetryCounts; counts != nil {
		return counts[retryDurationBucket(duration)]
	}
	return flakyRetries.RetryCount
}

// retryDurationBucket returns the EFD slow test retries bucket for a test duration, which dynamic automatic test
// retries share. The upper bounds stay exclusive as in the original EFD code; dd-trace-py uses inclusive ones, but
// matching it would change EFD bucketing.
func retryDurationBucket(duration time.Duration) int {
	switch {
	case duration < 5*time.Second:
		return 0
	case duration < 10*time.Second:
		return 1
	case duration < 30*time.Second:
		return 2
	case duration < 5*time.Minute:
		return 3
	default:
		return 4
	}
}

func efdHasPossibleRetry(settings *civisibilitynet.SettingsResponseData) bool {
	if settings == nil {
		return false
	}
	retries := settings.EarlyFlakeDetection.SlowTestRetries
	return retries.FiveS > 0 || retries.TenS > 0 || retries.ThirtyS > 0 || retries.FiveM > 0
}

// retryExecutionIsLast reports whether the current retry is the last one for
// a recognized retry family. Callers retain their existing behavior when no
// retry family is active by consulting the second return value.
func retryExecutionIsLast(execMeta *testExecutionMetadata, remainingRetries, remainingBudget int64) (bool, bool) {
	if execMeta == nil {
		return false, false
	}
	if execMeta.isAttemptToFix && execMeta.shouldOrchestrateAttemptToFix {
		return remainingRetries == 1, true
	}
	if usesEfdRetrySemantics(execMeta) {
		return remainingRetries == 1, true
	}
	if execMeta.isFlakyTestRetriesEnabled {
		return remainingRetries == 1 || remainingBudget == 0, true
	}
	return false, false
}

// willRetryAfterExecution mirrors postShouldRetry logic to determine if another retry
// will happen after the current execution. The skipped state is needed because
// clean skipped EFD executions are terminal even when retry budget remains.
func willRetryAfterExecution(failed, skipped bool, execMeta *testExecutionMetadata, remainingRetries int64, remainingBudget int64) bool {
	if execMeta.isAttemptToFix && execMeta.shouldOrchestrateAttemptToFix {
		// For attempt-to-fix tests, retry if remaining retries > 0.
		return remainingRetries > 0
	}

	if usesEfdRetrySemantics(execMeta) {
		// For EFD, retry executions that are not clean skips.
		cleanSkip := skipped && !failed
		return !cleanSkip && remainingRetries >= 0
	}

	if execMeta.isFlakyTestRetriesEnabled {
		// For flaky test retries, the per-test retry counter and global retry
		// budget must both reserve a retry before another attempt can run.
		return failed && remainingRetries >= 0 && remainingBudget > 0
	}

	// No retries for other cases.
	return false
}

// isFinalExecution determines if the current execution is the final one (no more retries will happen).
// This must be called after the test has completed and we know the result.
func isFinalExecution(failed, skipped bool, execMeta *testExecutionMetadata, duration time.Duration) bool {
	// If there's no additional feature wrapper, this is a single execution.
	if !execMeta.hasAdditionalFeatureWrapper {
		return true
	}

	// ATF takes precedence over parallel EFD - ATF tests should always compute final status
	// even when parallel EFD is enabled, because ATF orchestrates its own retry loop.
	isAtfExecution := execMeta.isAttemptToFix && execMeta.shouldOrchestrateAttemptToFix
	var remainingRetries int64
	if execMeta.isARetry {
		// For retries, use the captured remaining retries minus 1 (the decrement happens after span close).
		remainingRetries = execMeta.remainingRetries - 1
	} else {
		// For the initial execution, compute the retry count that would be set.
		// Subtract 1 because the decrement happens before postShouldRetry is called.
		remainingRetries = computeAdjustedRetryCount(execMeta, duration) - 1
	}

	// Parallel EFD: skip final status tagging.
	// All parallel executions capture the same remainingRetries, making it impossible
	// to determine which one is truly final. However, ATF tests are excluded from this
	// because they have their own retry orchestration.
	if execMeta.isEfdInParallel && !isAtfExecution {
		return false
	}
	if execMeta.retryContinuationDecided {
		return !execMeta.retryContinuationAdmitted
	}

	remainingBudget := int64(0)
	if usesFlakyRetryBudget(execMeta) {
		remainingBudget = atomic.LoadInt64(&integrations.GetFlakyRetriesSettings().RemainingTotalRetryCount)
	}
	// Legacy callers that do not own retry admission use a read-only projection.
	willRetry := willRetryAfterExecution(failed, skipped, execMeta, remainingRetries, remainingBudget)
	return !willRetry
}

func tryReserveFlakyRetryBudget() bool {
	remaining := &integrations.GetFlakyRetriesSettings().RemainingTotalRetryCount
	for {
		current := atomic.LoadInt64(remaining)
		if current <= 0 {
			return false
		}
		if atomic.CompareAndSwapInt64(remaining, current, current-1) {
			return true
		}
	}
}

func flakyRetryBudgetRemaining(settings *integrations.FlakyRetriesSetting) int64 {
	if settings == nil {
		return 0
	}
	return atomic.LoadInt64(&settings.RemainingTotalRetryCount)
}

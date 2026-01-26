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
)

// calculateFinalStatus computes the test.final_status value based on the overall test outcome.
// Priority order: quarantined/disabled -> pass (any pass wins) -> fail (any fail) -> skip -> fail.
func calculateFinalStatus(anyPassed, anyFailed, currentIsSkip, isQuarantined, isDisabled bool) string {
	if isQuarantined || isDisabled {
		return constants.TestStatusSkip
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

// computeAdjustedRetryCount mirrors postAdjustRetryCount logic to determine the retry count
// for the initial execution (before the first run completes). This allows us to predict
// whether more retries will happen.
func computeAdjustedRetryCount(execMeta *testExecutionMetadata, duration time.Duration) int64 {
	settings := integrations.GetSettings()

	// Attempt To Fix retries are always set to the configured value.
	if execMeta.isAttemptToFix && execMeta.shouldOrchestrateAttemptToFix {
		return int64(settings.TestManagement.AttemptToFixRetries)
	}

	// Early Flake Detection adjusts the retry count based on test duration.
	if isAnEfdExecution(execMeta) {
		slowTestRetries := settings.EarlyFlakeDetection.SlowTestRetries
		secs := duration.Seconds()
		if secs < 5 {
			return int64(slowTestRetries.FiveS)
		} else if secs < 10 {
			return int64(slowTestRetries.TenS)
		} else if secs < 30 {
			return int64(slowTestRetries.ThirtyS)
		} else if duration.Minutes() < 5 {
			return int64(slowTestRetries.FiveM)
		}
	}

	// Automatic flaky tests retries are set to the configured value.
	if execMeta.isFlakyTestRetriesEnabled {
		return integrations.GetFlakyRetriesSettings().RetryCount
	}

	// No retries
	return 0
}

// willRetryAfterExecution mirrors postShouldRetry logic to determine if another retry
// will happen after the current execution.
func willRetryAfterExecution(failed bool, execMeta *testExecutionMetadata, remainingRetries int64, remainingBudget int64) bool {
	if execMeta.isAttemptToFix && execMeta.shouldOrchestrateAttemptToFix {
		// For attempt-to-fix tests, retry if remaining retries > 0.
		return remainingRetries > 0
	}

	if isAnEfdExecution(execMeta) {
		// For EFD, retry if remaining retries >= 0.
		return remainingRetries >= 0
	}

	if execMeta.isFlakyTestRetriesEnabled {
		// For flaky test retries, retry if the test failed and remaining retries >= 0.
		return failed && remainingRetries >= 0 && remainingBudget >= 0
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

	// Parallel EFD: skip final status tagging (Option A from the plan).
	// All parallel executions capture the same remainingRetries, making it impossible
	// to determine which one is truly final. However, ATF tests are excluded from this
	// because they have their own retry orchestration.
	if execMeta.isEfdInParallel && !isAtfExecution {
		return false
	}

	var remainingRetries int64
	var remainingBudget int64

	if execMeta.isARetry {
		// For retries, use the captured remaining retries minus 1 (the decrement happens after span close).
		remainingRetries = execMeta.remainingRetries - 1

		// For flaky retries, also account for the global budget decrement.
		if execMeta.isFlakyTestRetriesEnabled {
			remainingBudget = atomic.LoadInt64(&integrations.GetFlakyRetriesSettings().RemainingTotalRetryCount) - 1
		} else {
			remainingBudget = 0
		}
	} else {
		// For the initial execution, compute the retry count that would be set.
		// Subtract 1 because the decrement happens before postShouldRetry is called.
		remainingRetries = computeAdjustedRetryCount(execMeta, duration) - 1
		if execMeta.isFlakyTestRetriesEnabled {
			// For the initial execution, the global budget isn't decremented in postPerExecution,
			// but we need to check what postShouldRetry will see.
			remainingBudget = atomic.LoadInt64(&integrations.GetFlakyRetriesSettings().RemainingTotalRetryCount)
		} else {
			remainingBudget = 0
		}
	}

	// Check if another retry would happen.
	willRetry := willRetryAfterExecution(failed, execMeta, remainingRetries, remainingBudget)
	return !willRetry
}

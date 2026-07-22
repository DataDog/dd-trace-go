// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"fmt"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
)

func finalizeInstrumentedTestExecution(
	t *testing.T,
	execMeta *testExecutionMetadata,
	test integrations.Test,
	suite integrations.TestSuite,
	module integrations.TestModule,
	duration time.Duration,
	attemptOutput []byte,
	terminal any,
	terminalStack string,
	markContainersOnTerminal bool,
) {
	if execMeta.isANewTest && duration.Minutes() >= 5 {
		test.SetTag(constants.TestEarlyFlakeDetectionRetryAborted, "slow")
	}

	collectAndWriteLogs(t, test, attemptOutput)
	if terminal != nil {
		t.Fail()
		execMeta.panicData = terminal
		execMeta.panicStacktrace = terminalStack
		finalExec := isFinalExecution(true, false, execMeta, duration)
		if finalExec {
			finalStatus := calculateFinalStatus(execMeta.anyExecutionPassed, true, false, execMeta.isQuarantined, execMeta.isDisabled, execMeta.isAttemptToFix)
			test.SetTag(constants.TestFinalStatus, finalStatus)
		}
		if execMeta.isARetry && finalExec {
			if execMeta.allRetriesFailed {
				test.SetTag(constants.TestHasFailedAllRetries, "true")
			}
			if execMeta.isAttemptToFix {
				test.SetTag(constants.TestAttemptToFixPassed, "false")
			}
		}
		test.SetError(integrations.WithErrorInfo("panic", fmt.Sprint(terminal), terminalStack))
		if markContainersOnTerminal {
			suite.SetTag(ext.Error, true)
			module.SetTag(ext.Error, true)
		}
		test.Close(integrations.ResultStatusFail)
		return
	}

	failed := t.Failed()
	skipped := t.Skipped()
	finalExec := isFinalExecution(failed, skipped, execMeta, duration)
	switch {
	case failed:
		if finalExec {
			finalStatus := calculateFinalStatus(execMeta.anyExecutionPassed, true, false, execMeta.isQuarantined, execMeta.isDisabled, execMeta.isAttemptToFix)
			test.SetTag(constants.TestFinalStatus, finalStatus)
		}
		if execMeta.isARetry && finalExec {
			if execMeta.allRetriesFailed {
				test.SetTag(constants.TestHasFailedAllRetries, "true")
			}
			if execMeta.isAttemptToFix {
				test.SetTag(constants.TestAttemptToFixPassed, "false")
			}
		}
		if execMeta.panicData != nil {
			test.SetError(integrations.WithErrorInfo("panic", fmt.Sprint(execMeta.panicData), execMeta.panicStacktrace))
		} else {
			test.SetTag(ext.Error, true)
		}
		suite.SetTag(ext.Error, true)
		module.SetTag(ext.Error, true)
		test.Close(integrations.ResultStatusFail)
	case skipped:
		if finalExec {
			finalStatus := calculateFinalStatus(execMeta.anyExecutionPassed, execMeta.anyExecutionFailed, true, execMeta.isQuarantined, execMeta.isDisabled, execMeta.isAttemptToFix)
			test.SetTag(constants.TestFinalStatus, finalStatus)
		}
		if execMeta.isAttemptToFix && execMeta.isARetry && finalExec {
			test.SetTag(constants.TestAttemptToFixPassed, "false")
		}
		if execMeta.skipReason != "" {
			test.Close(integrations.ResultStatusSkip, integrations.WithTestSkipReason(execMeta.skipReason))
		} else {
			test.Close(integrations.ResultStatusSkip)
		}
	default:
		if finalExec {
			finalStatus := calculateFinalStatus(true, execMeta.anyExecutionFailed, false, execMeta.isQuarantined, execMeta.isDisabled, execMeta.isAttemptToFix)
			test.SetTag(constants.TestFinalStatus, finalStatus)
		}
		if execMeta.isAttemptToFix && execMeta.isARetry && finalExec {
			if execMeta.allAttemptsPassed {
				test.SetTag(constants.TestAttemptToFixPassed, "true")
			} else {
				test.SetTag(constants.TestAttemptToFixPassed, "false")
			}
		}
		test.Close(integrations.ResultStatusPass)
	}
}

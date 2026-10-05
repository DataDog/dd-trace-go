// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package civisibility_failnow_retry

import (
	"os"
	"sync/atomic"
	"testing"

	"github.com/DataDog/orchestrion/runtime/built"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/civisibilitytest"
)

var ciVisibilityPayloads *civisibilitytest.Payloads

var (
	// cleanupPanicAttempts tracks executions for the cleanup panic retry scenario.
	cleanupPanicAttempts atomic.Int32
	// cleanupFatalAttempts tracks executions for the cleanup fatal retry scenario.
	cleanupFatalAttempts atomic.Int32
)

func TestMain(m *testing.M) {
	if !built.WithOrchestrion {
		panic("Orchestrion is not enabled, please run this test with orchestrion")
	}

	settings := net.SettingsResponseData{FlakyTestRetriesEnabled: true}
	server, payloads, restore := civisibilitytest.StartMockServer(settings)
	defer restore()
	_ = server
	ciVisibilityPayloads = payloads
	_ = os.Setenv("DD_CIVISIBILITY_FLAKY_RETRY_COUNT", "2")
	_ = os.Setenv("DD_CIVISIBILITY_TOTAL_FLAKY_RETRY_COUNT", "10")

	exitCode := m.Run()
	if exitCode != 1 {
		panic("expected m.Run to fail with exit code 1")
	}

	validateRetryEvents(ciVisibilityPayloads.Events())
	os.Exit(0)
}

func TestRetryFatalAlwaysFails(t *testing.T) {
	t.Fatal("retry fatal")
}

func TestCleanupPanicRetryPasses(t *testing.T) {
	attempt := cleanupPanicAttempts.Add(1)
	t.Cleanup(func() {
		if attempt == 1 {
			panic("cleanup panic")
		}
	})
}

func TestCleanupFatalRetryPasses(t *testing.T) {
	attempt := cleanupFatalAttempts.Add(1)
	t.Cleanup(func() {
		if attempt == 1 {
			t.Fatal("cleanup fatal")
		}
	})
}

func TestCleanupSkipDoesNotRetry(t *testing.T) {
	t.Cleanup(func() {
		t.Skip("cleanup skip")
	})
}

func TestAfterRetryFatal(t *testing.T) {}

func validateRetryEvents(events civisibilitytest.Events) {
	events.CheckEventsByType("test_session_end", 1).
		CheckEventsByTagAndValue("test.status", "fail", 1).
		CheckEventsByTagAndValue("error.type", "ExitCode", 1).
		CheckEventsByTagAndValue("error.message", "exit code is not zero.", 1).
		CheckEventsByMetricAndValue("test.exit_code", 1, 1)
	events.CheckEventsByType("test_module_end", 1)
	events.CheckEventsByType("test_suite_end", 1)

	testEvents := events.CheckEventsByType("test", 9)
	retries := testEvents.
		CheckEventsByResourceName("testing_test.go.TestRetryFatalAlwaysFails", 3).
		CheckEventsByTagAndValue("test.status", "fail", 3).
		CheckEventsByTagAndValue("error.type", "Fatal", 3).
		CheckEventsByTagAndValue("error.message", "retry fatal", 3)
	retries.CheckEventsByTagAndValue("test.is_retry", "true", 2)
	retries.CheckEventsByTagAndValue("test.retry_reason", "auto_test_retry", 2)
	retries.CheckEventsByTagAndValue("test.has_failed_all_retries", "true", 1)
	retries.CheckEventsByTagAndValue("test.final_status", "fail", 1)
	cleanupPanic := testEvents.
		CheckEventsByResourceName("testing_test.go.TestCleanupPanicRetryPasses", 2)
	cleanupPanic.CheckEventsByTagAndValue("test.status", "fail", 1).
		CheckEventsByTagAndValue("error.type", "panic", 1).
		CheckEventsByTagAndValue("error.message", "cleanup panic", 1)
	cleanupPanic.CheckEventsByTagAndValue("test.status", "pass", 1).
		CheckEventsByTagAndValue("test.final_status", "pass", 1)
	cleanupPanic.CheckEventsByTagAndValue("test.is_retry", "true", 1)
	cleanupPanic.CheckEventsByTagAndValue("test.retry_reason", "auto_test_retry", 1)
	cleanupFatal := testEvents.
		CheckEventsByResourceName("testing_test.go.TestCleanupFatalRetryPasses", 2)
	cleanupFatal.CheckEventsByTagAndValue("test.status", "fail", 1)
	cleanupFatal.CheckEventsByTagAndValue("test.status", "pass", 1).
		CheckEventsByTagAndValue("test.final_status", "pass", 1)
	cleanupFatal.CheckEventsByTagAndValue("test.is_retry", "true", 1)
	cleanupFatal.CheckEventsByTagAndValue("test.retry_reason", "auto_test_retry", 1)
	cleanupSkip := testEvents.
		CheckEventsByResourceName("testing_test.go.TestCleanupSkipDoesNotRetry", 1).
		CheckEventsByTagAndValue("test.status", "skip", 1).
		CheckEventsByTagAndValue("test.final_status", "skip", 1).
		CheckEventsWithoutTag("test.is_retry", 1).
		CheckEventsWithoutTag("test.retry_reason", 1)
	after := testEvents.
		CheckEventsByResourceName("testing_test.go.TestAfterRetryFatal", 1).
		CheckEventsByTagAndValue("test.status", "pass", 1)

	testEvents.Except(retries, cleanupPanic, cleanupFatal, cleanupSkip, after).HasCount(0)
}

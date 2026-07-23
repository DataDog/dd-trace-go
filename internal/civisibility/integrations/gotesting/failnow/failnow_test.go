// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package failnow

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
	gotesting "github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting"
	civisibilitynet "github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
	"github.com/DataDog/dd-trace-go/v2/internal/env"
)

const scenarioEnv = "DD_FAILNOW_SCENARIO"

var (
	retryPassAttempts    atomic.Int32
	retryFailAttempts    atomic.Int32
	cleanupPanicAttempts atomic.Int32
	cleanupFatalAttempts atomic.Int32
)

func TestMain(m *testing.M) {
	if os.Getenv(scenarioEnv) == "" {
		os.Exit(m.Run())
	}
	os.Exit(runScenario(m))
}

func TestManualFailNow(t *testing.T) {
	runTestScenario(t, "test-failnow", "^TestManualFailNowFixture$")
}

func TestManualFatal(t *testing.T) {
	runTestScenario(t, "test-fatal", "^TestManualFatalFixture$")
}

func TestManualFatalf(t *testing.T) {
	runTestScenario(t, "test-fatalf", "^TestManualFatalfFixture$")
}

func TestManualFailNowRetryPasses(t *testing.T) {
	runTestScenario(t, "test-failnow-retry-passes", "^TestManualFailNowRetryPassesFixture$")
}

func TestManualFailNowRetryFails(t *testing.T) {
	runTestScenario(t, "test-failnow-retry-fails", "^TestManualFailNowRetryFailsFixture$")
}

func TestCleanupPanicRetryPasses(t *testing.T) {
	runTestScenario(t, "test-cleanup-panic-retry-passes", "^TestCleanupPanicRetryPassesFixture$")
}

func TestCleanupFatalRetryPasses(t *testing.T) {
	runTestScenario(t, "test-cleanup-fatal-retry-passes", "^TestCleanupFatalRetryPassesFixture$")
}

// TestCleanupSkipDoesNotRetry verifies cleanup skips stay skipped instead of
// being converted into retryable failures.
func TestCleanupSkipDoesNotRetry(t *testing.T) {
	runTestScenario(t, "test-cleanup-skip-does-not-retry", "^TestCleanupSkipDoesNotRetryFixture$")
}

func TestCleanupRunsAfterParallelSubtest(t *testing.T) {
	runTestScenario(t, "test-cleanup-after-parallel-subtest", "^TestCleanupRunsAfterParallelSubtestFixture$")
}

// TestParallelSubtestSchedulerSlotIsReleased verifies Datadog-managed retry
// clones release their parent scheduler slot before waiting for parallel
// subtests. With -parallel=1, failing to release that slot deadlocks the child
// in testing.(*testState).waitParallel until the package timeout fires.
func TestParallelSubtestSchedulerSlotIsReleased(t *testing.T) {
	runSubprocess(t, "test-cleanup-after-parallel-subtest", "-test.run", "^TestCleanupRunsAfterParallelSubtestFixture$", "-test.parallel=1", "-test.timeout=5s")
}

func TestFlakyRetryGlobalBudget(t *testing.T) {
	runTestScenario(t, "test-flaky-retry-global-budget", "^TestFlakyRetryGlobalBudgetFixture$")
}

func TestManualBenchmarkFailNow(t *testing.T) {
	runBenchmarkScenario(t, "benchmark-failnow", "^BenchmarkManualFailNowFixture$")
}

func TestManualBenchmarkFatal(t *testing.T) {
	runBenchmarkScenario(t, "benchmark-fatal", "^BenchmarkManualFatalFixture$")
}

func TestManualBenchmarkFatalf(t *testing.T) {
	runBenchmarkScenario(t, "benchmark-fatalf", "^BenchmarkManualFatalfFixture$")
}

func TestManualFailNowFixture(t *testing.T) {
	skipUnlessScenario(t, "test-failnow")
	gt := gotesting.GetTest(t)
	ctx := gt.Context()
	t.Cleanup(func() { finishCleanupSpan(ctx, "manual.failnow.cleanup") })
	gt.FailNow()
}

func TestManualFatalFixture(t *testing.T) {
	skipUnlessScenario(t, "test-fatal")
	gt := gotesting.GetTest(t)
	ctx := gt.Context()
	t.Cleanup(func() { finishCleanupSpan(ctx, "manual.fatal.cleanup") })
	gt.Fatal("manual fatal")
}

func TestManualFatalfFixture(t *testing.T) {
	skipUnlessScenario(t, "test-fatalf")
	gt := gotesting.GetTest(t)
	ctx := gt.Context()
	t.Cleanup(func() { finishCleanupSpan(ctx, "manual.fatalf.cleanup") })
	gt.Fatalf("manual %s", "fatalf")
}

func TestManualFailNowRetryPassesFixture(t *testing.T) {
	skipUnlessScenario(t, "test-failnow-retry-passes")
	gt := gotesting.GetTest(t)
	ctx := gt.Context()
	t.Cleanup(func() { finishCleanupSpan(ctx, "manual.failnow.retry.passes.cleanup") })
	if retryPassAttempts.Add(1) < 3 {
		gt.FailNow()
	}
}

func TestManualFailNowRetryFailsFixture(t *testing.T) {
	skipUnlessScenario(t, "test-failnow-retry-fails")
	gt := gotesting.GetTest(t)
	ctx := gt.Context()
	t.Cleanup(func() { finishCleanupSpan(ctx, "manual.failnow.retry.fails.cleanup") })
	retryFailAttempts.Add(1)
	gt.FailNow()
}

func TestCleanupPanicRetryPassesFixture(t *testing.T) {
	skipUnlessScenario(t, "test-cleanup-panic-retry-passes")
	if cleanupPanicAttempts.Add(1) == 1 {
		t.Cleanup(func() {
			panic("cleanup panic")
		})
	}
}

func TestCleanupFatalRetryPassesFixture(t *testing.T) {
	skipUnlessScenario(t, "test-cleanup-fatal-retry-passes")
	if cleanupFatalAttempts.Add(1) == 1 {
		t.Cleanup(func() {
			t.Fatal("cleanup fatal")
		})
	}
}

// TestCleanupSkipDoesNotRetryFixture skips from cleanup while flaky retries are
// enabled; a clean skip should not consume retry attempts or fail the process.
func TestCleanupSkipDoesNotRetryFixture(t *testing.T) {
	skipUnlessScenario(t, "test-cleanup-skip-does-not-retry")
	t.Cleanup(func() {
		t.Skip("cleanup skip")
	})
}

func TestCleanupRunsAfterParallelSubtestFixture(t *testing.T) {
	skipUnlessScenario(t, "test-cleanup-after-parallel-subtest")

	var childFinished atomic.Bool
	t.Cleanup(func() {
		if !childFinished.Load() {
			t.Fatal("cleanup ran before the parallel subtest completed")
		}
	})

	gotesting.GetTest(t).Run("child", func(t *testing.T) {
		gotesting.GetTest(t).Parallel()
		childFinished.Store(true)
	})
}

func TestFlakyRetryGlobalBudgetFixture(t *testing.T) {
	skipUnlessScenario(t, "test-flaky-retry-global-budget")
	gotesting.GetTest(t).Fail()
}

func BenchmarkManualFailNowFixture(b *testing.B) {
	skipUnlessScenario(b, "benchmark-failnow")
	gb := gotesting.GetBenchmark(b)
	ctx := gb.Context()
	b.Cleanup(func() { finishCleanupSpan(ctx, "manual.benchmark.failnow.cleanup") })
	gb.FailNow()
}

func BenchmarkManualFatalFixture(b *testing.B) {
	skipUnlessScenario(b, "benchmark-fatal")
	gb := gotesting.GetBenchmark(b)
	ctx := gb.Context()
	b.Cleanup(func() { finishCleanupSpan(ctx, "manual.benchmark.fatal.cleanup") })
	gb.Fatal("manual fatal")
}

func BenchmarkManualFatalfFixture(b *testing.B) {
	skipUnlessScenario(b, "benchmark-fatalf")
	gb := gotesting.GetBenchmark(b)
	ctx := gb.Context()
	b.Cleanup(func() { finishCleanupSpan(ctx, "manual.benchmark.fatalf.cleanup") })
	gb.Fatalf("manual %s", "fatalf")
}

func runScenario(m *testing.M) int {
	scenario := os.Getenv(scenarioEnv)
	settings := civisibilitynet.SettingsResponseData{}
	if scenario == "test-failnow-retry-passes" || scenario == "test-failnow-retry-fails" ||
		scenario == "test-cleanup-panic-retry-passes" || scenario == "test-cleanup-fatal-retry-passes" ||
		scenario == "test-cleanup-skip-does-not-retry" || scenario == "test-cleanup-after-parallel-subtest" {
		settings.FlakyTestRetriesEnabled = true
		_ = os.Setenv(constants.CIVisibilityFlakyRetryCountEnvironmentVariable, "2")
		_ = os.Setenv(constants.CIVisibilityTotalFlakyRetryCountEnvironmentVariable, "10")
	}
	if scenario == "test-flaky-retry-global-budget" {
		settings.FlakyTestRetriesEnabled = true
		_ = os.Setenv(constants.CIVisibilityFlakyRetryCountEnvironmentVariable, "10")
		_ = os.Setenv(constants.CIVisibilityTotalFlakyRetryCountEnvironmentVariable, "1")
	}
	server, restore := startManualMockServer(settings)
	defer restore()
	_ = server

	mt := integrations.InitializeCIVisibilityMock()
	exitCode := gotesting.RunM(m)
	validateScenario(scenario, mt.FinishedSpans(), exitCode)
	return 0
}

func runTestScenario(t *testing.T, scenario, pattern string) {
	runSubprocess(t, scenario, "-test.run", pattern)
}

func runBenchmarkScenario(t *testing.T, scenario, pattern string) {
	runSubprocess(t, scenario, "-test.run", "^$", "-test.bench", pattern, "-test.benchtime=1x")
}

func runSubprocess(t *testing.T, scenario string, args ...string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), scenarioEnv+"="+scenario)
	if scenario == "test-cleanup-skip-does-not-retry" {
		cmd.Env = append(cmd.Env, "DD_TRACE_DEBUG=true")
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("scenario %s failed: %v\n%s", scenario, err, output)
	}
}

func skipUnlessScenario(tb testing.TB, scenario string) {
	tb.Helper()
	if os.Getenv(scenarioEnv) != scenario {
		tb.Skip("fixture runs only in its subprocess scenario")
	}
}

func finishCleanupSpan(ctx context.Context, resource string) {
	span, _ := tracer.StartSpanFromContext(ctx, "manual.cleanup", tracer.ResourceName(resource))
	if span != nil {
		span.Finish()
	}
}

func startManualMockServer(settings civisibilitynet.SettingsResponseData) (*httptest.Server, func()) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/libraries/tests/services/setting":
			w.Header().Set("Content-Type", "application/json")
			response := struct {
				Data struct {
					ID         string                               `json:"id"`
					Type       string                               `json:"type"`
					Attributes civisibilitynet.SettingsResponseData `json:"attributes"`
				} `json:"data"`
			}{}
			response.Data.ID = "settings"
			response.Data.Type = "ci_app_libraries_tests_settings"
			response.Data.Attributes = settings
			_ = json.NewEncoder(w).Encode(response)
		case "/api/v2/git/repository/search_commits":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{}"))
		case "/api/v2/git/repository/packfile", "/api/v2/logs":
			w.WriteHeader(http.StatusAccepted)
		default:
			http.NotFound(w, r)
		}
	}))

	restore := setEnv(map[string]string{
		constants.CIVisibilityAgentlessEnabledEnvironmentVariable: "1",
		constants.CIVisibilityAgentlessURLEnvironmentVariable:     server.URL,
		constants.APIKeyEnvironmentVariable:                       "12345",
		"DD_GIT_REPOSITORY_URL":                                   "https://github.com/DataDog/dd-trace-go.git",
		"DD_GIT_COMMIT_SHA":                                       "1234567890abcdef1234567890abcdef12345678",
		"DD_GIT_BRANCH":                                           "main",
	})
	return server, func() {
		restore()
		server.Close()
	}
}

func validateScenario(scenario string, spans []*mocktracer.Span, exitCode int) {
	switch scenario {
	case "test-failnow":
		validateFailureScenario(spans, exitCode, "failnow_test.go.TestManualFailNowFixture", "manual.failnow.cleanup", "FailNow", "failed test", true)
	case "test-fatal":
		validateFailureScenario(spans, exitCode, "failnow_test.go.TestManualFatalFixture", "manual.fatal.cleanup", "Fatal", "manual fatal", true)
	case "test-fatalf":
		validateFailureScenario(spans, exitCode, "failnow_test.go.TestManualFatalfFixture", "manual.fatalf.cleanup", "Fatalf", "manual fatalf", true)
	case "benchmark-failnow":
		validateFailureScenario(spans, exitCode, "failnow_test.go.BenchmarkManualFailNowFixture", "manual.benchmark.failnow.cleanup", "FailNow", "failed test", false)
	case "benchmark-fatal":
		validateFailureScenario(spans, exitCode, "failnow_test.go.BenchmarkManualFatalFixture", "manual.benchmark.fatal.cleanup", "Fatal", "manual fatal", false)
	case "benchmark-fatalf":
		validateFailureScenario(spans, exitCode, "failnow_test.go.BenchmarkManualFatalfFixture", "manual.benchmark.fatalf.cleanup", "Fatalf", "manual fatalf", false)
	case "test-failnow-retry-passes":
		validateRetryPassScenario(spans, exitCode)
	case "test-failnow-retry-fails":
		validateRetryFailScenario(spans, exitCode)
	case "test-cleanup-panic-retry-passes":
		validateCleanupRetryPassScenario(spans, exitCode, "failnow_test.go.TestCleanupPanicRetryPassesFixture", "cleanup panic")
	case "test-cleanup-fatal-retry-passes":
		validateCleanupRetryPassScenario(spans, exitCode, "failnow_test.go.TestCleanupFatalRetryPassesFixture", "")
	case "test-cleanup-skip-does-not-retry":
		validateCleanupSkipDoesNotRetryScenario(spans, exitCode)
	case "test-cleanup-after-parallel-subtest":
		validateCleanupAfterParallelSubtestScenario(spans, exitCode)
	case "test-flaky-retry-global-budget":
		validateGlobalBudgetScenario(spans, exitCode)
	default:
		panic("unknown scenario " + scenario)
	}
}

func validateFailureScenario(spans []*mocktracer.Span, exitCode int, resource, cleanupResource, errorType, errorMessage string, expectFinalStatus bool) {
	assertEqual("exit code", exitCode, 1)
	assertSpanTypeCount(spans, constants.SpanTypeTestSession, 1)
	assertSpanTypeCount(spans, constants.SpanTypeTestModule, 1)
	assertSpanTypeCount(spans, constants.SpanTypeTestSuite, 1)

	testSpans := spansByResource(spansByType(spans, constants.SpanTypeTest), resource)
	assertEqual("test span count", len(testSpans), 1)
	assertTag(testSpans[0], constants.TestStatus, constants.TestStatusFail)
	if expectFinalStatus {
		assertTag(testSpans[0], constants.TestFinalStatus, constants.TestStatusFail)
	}
	assertTag(testSpans[0], ext.ErrorType, errorType)
	assertTag(testSpans[0], ext.ErrorMsg, errorMessage)
	assertEqual("cleanup span count", len(spansByResource(spans, cleanupResource)), 1)

	session := spansByType(spans, constants.SpanTypeTestSession)[0]
	assertTag(session, constants.TestStatus, constants.TestStatusFail)
	assertTag(session, ext.ErrorType, "ExitCode")
	assertTag(session, ext.ErrorMsg, "exit code is not zero.")
	assertNumericTag(session, constants.TestCommandExitCode, 1)
}

func validateRetryPassScenario(spans []*mocktracer.Span, exitCode int) {
	assertEqual("exit code", exitCode, 0)
	assertSpanTypeCount(spans, constants.SpanTypeTestSession, 1)
	assertSpanTypeCount(spans, constants.SpanTypeTestModule, 1)
	assertSpanTypeCount(spans, constants.SpanTypeTestSuite, 1)

	session := spansByType(spans, constants.SpanTypeTestSession)[0]
	assertTag(session, constants.TestStatus, constants.TestStatusPass)
	assertNumericTag(session, constants.TestCommandExitCode, 0)

	resource := "failnow_test.go.TestManualFailNowRetryPassesFixture"
	testSpans := spansByResource(spansByType(spans, constants.SpanTypeTest), resource)
	assertEqual("test span count", len(testSpans), 3)
	assertEqual("cleanup span count", len(spansByResource(spans, "manual.failnow.retry.passes.cleanup")), 3)
	assertTagCount(testSpans, constants.TestIsRetry, "true", 2)
	assertTagCount(testSpans, constants.TestRetryReason, constants.AutoTestRetriesRetryReason, 2)
	assertTagCount(testSpans, constants.TestStatus, constants.TestStatusFail, 2)
	assertTagCount(testSpans, constants.TestStatus, constants.TestStatusPass, 1)
	assertTagCount(testSpans, constants.TestFinalStatus, constants.TestStatusPass, 1)
	assertTagCount(testSpans, constants.TestHasFailedAllRetries, "true", 0)
}

func validateRetryFailScenario(spans []*mocktracer.Span, exitCode int) {
	assertEqual("exit code", exitCode, 1)
	assertSpanTypeCount(spans, constants.SpanTypeTestSession, 1)
	assertSpanTypeCount(spans, constants.SpanTypeTestModule, 1)
	assertSpanTypeCount(spans, constants.SpanTypeTestSuite, 1)

	session := spansByType(spans, constants.SpanTypeTestSession)[0]
	assertTag(session, constants.TestStatus, constants.TestStatusFail)
	assertTag(session, ext.ErrorType, "ExitCode")
	assertTag(session, ext.ErrorMsg, "exit code is not zero.")
	assertNumericTag(session, constants.TestCommandExitCode, 1)

	resource := "failnow_test.go.TestManualFailNowRetryFailsFixture"
	testSpans := spansByResource(spansByType(spans, constants.SpanTypeTest), resource)
	assertEqual("test span count", len(testSpans), 3)
	assertEqual("cleanup span count", len(spansByResource(spans, "manual.failnow.retry.fails.cleanup")), 3)
	assertTagCount(testSpans, constants.TestIsRetry, "true", 2)
	assertTagCount(testSpans, constants.TestRetryReason, constants.AutoTestRetriesRetryReason, 2)
	assertTagCount(testSpans, constants.TestStatus, constants.TestStatusFail, 3)
	assertTagCount(testSpans, constants.TestFinalStatus, constants.TestStatusFail, 1)
	assertTagCount(testSpans, constants.TestHasFailedAllRetries, "true", 1)
}

func validateCleanupRetryPassScenario(spans []*mocktracer.Span, exitCode int, resource, expectedPanicMessage string) {
	assertEqual("exit code", exitCode, 0)
	assertSpanTypeCount(spans, constants.SpanTypeTestSession, 1)
	assertSpanTypeCount(spans, constants.SpanTypeTestModule, 1)
	assertSpanTypeCount(spans, constants.SpanTypeTestSuite, 1)

	session := spansByType(spans, constants.SpanTypeTestSession)[0]
	assertTag(session, constants.TestStatus, constants.TestStatusPass)
	assertNumericTag(session, constants.TestCommandExitCode, 0)

	testSpans := spansByResource(spansByType(spans, constants.SpanTypeTest), resource)
	assertEqual("test span count", len(testSpans), 2)
	assertTagCount(testSpans, constants.TestIsRetry, "true", 1)
	assertTagCount(testSpans, constants.TestRetryReason, constants.AutoTestRetriesRetryReason, 1)
	assertTagCount(testSpans, constants.TestStatus, constants.TestStatusFail, 1)
	assertTagCount(testSpans, constants.TestStatus, constants.TestStatusPass, 1)
	assertTagCount(testSpans, constants.TestFinalStatus, constants.TestStatusPass, 1)
	assertTagCount(testSpans, constants.TestHasFailedAllRetries, "true", 0)
	if expectedPanicMessage != "" {
		for _, span := range testSpans {
			if span.Tag(constants.TestStatus) == constants.TestStatusFail {
				assertTag(span, ext.ErrorType, "panic")
				assertTag(span, ext.ErrorMsg, expectedPanicMessage)
				return
			}
		}
		panic("expected one failed cleanup panic span")
	}
}

func validateCleanupAfterParallelSubtestScenario(spans []*mocktracer.Span, exitCode int) {
	assertEqual("exit code", exitCode, 0)
	assertSpanTypeCount(spans, constants.SpanTypeTestSession, 1)
	assertSpanTypeCount(spans, constants.SpanTypeTestModule, 1)
	assertSpanTypeCount(spans, constants.SpanTypeTestSuite, 1)

	session := spansByType(spans, constants.SpanTypeTestSession)[0]
	assertTag(session, constants.TestStatus, constants.TestStatusPass)
	assertNumericTag(session, constants.TestCommandExitCode, 0)

	resource := "failnow_test.go.TestCleanupRunsAfterParallelSubtestFixture"
	testSpans := spansByResource(spansByType(spans, constants.SpanTypeTest), resource)
	assertEqual("test span count", len(testSpans), 1)
	assertTag(testSpans[0], constants.TestStatus, constants.TestStatusPass)
	assertTag(testSpans[0], constants.TestFinalStatus, constants.TestStatusPass)
}

// validateCleanupSkipDoesNotRetryScenario confirms cleanup Skip is reported as
// a single skipped attempt even when flaky retries are available.
func validateCleanupSkipDoesNotRetryScenario(spans []*mocktracer.Span, exitCode int) {
	assertEqual("exit code", exitCode, 0)
	assertSpanTypeCount(spans, constants.SpanTypeTestSession, 1)
	assertSpanTypeCount(spans, constants.SpanTypeTestModule, 1)
	assertSpanTypeCount(spans, constants.SpanTypeTestSuite, 1)

	session := spansByType(spans, constants.SpanTypeTestSession)[0]
	assertTag(session, constants.TestStatus, constants.TestStatusPass)
	assertNumericTag(session, constants.TestCommandExitCode, 0)

	resource := "failnow_test.go.TestCleanupSkipDoesNotRetryFixture"
	testSpans := spansByResource(spansByType(spans, constants.SpanTypeTest), resource)
	assertEqual("test span count", len(testSpans), 1)
	assertTag(testSpans[0], constants.TestStatus, constants.TestStatusSkip)
	assertTag(testSpans[0], constants.TestFinalStatus, constants.TestStatusSkip)
	assertTagCount(testSpans, constants.TestIsRetry, "true", 0)
}

func validateGlobalBudgetScenario(spans []*mocktracer.Span, exitCode int) {
	assertEqual("exit code", exitCode, 1)
	assertSpanTypeCount(spans, constants.SpanTypeTestSession, 1)
	assertSpanTypeCount(spans, constants.SpanTypeTestModule, 1)
	assertSpanTypeCount(spans, constants.SpanTypeTestSuite, 1)

	session := spansByType(spans, constants.SpanTypeTestSession)[0]
	assertTag(session, constants.TestStatus, constants.TestStatusFail)
	assertNumericTag(session, constants.TestCommandExitCode, 1)

	resource := "failnow_test.go.TestFlakyRetryGlobalBudgetFixture"
	testSpans := spansByResource(spansByType(spans, constants.SpanTypeTest), resource)
	assertEqual("test span count", len(testSpans), 2)
	assertTagCount(testSpans, constants.TestIsRetry, "true", 1)
	assertTagCount(testSpans, constants.TestRetryReason, constants.AutoTestRetriesRetryReason, 1)
	assertTagCount(testSpans, constants.TestStatus, constants.TestStatusFail, 2)
	assertTagCount(testSpans, constants.TestFinalStatus, constants.TestStatusFail, 1)
	assertTagCount(testSpans, constants.TestHasFailedAllRetries, "true", 1)
}

func spansByType(spans []*mocktracer.Span, spanType string) []*mocktracer.Span {
	var result []*mocktracer.Span
	for _, span := range spans {
		if span.Tag(ext.SpanType) == spanType {
			result = append(result, span)
		}
	}
	return result
}

func spansByResource(spans []*mocktracer.Span, resource string) []*mocktracer.Span {
	var result []*mocktracer.Span
	for _, span := range spans {
		if span.Tag(ext.ResourceName) == resource {
			result = append(result, span)
		}
	}
	return result
}

func assertSpanTypeCount(spans []*mocktracer.Span, spanType string, expected int) {
	assertEqual("span type "+spanType, len(spansByType(spans, spanType)), expected)
}

func assertTag(span *mocktracer.Span, key string, expected any) {
	assertEqual("tag "+key, span.Tag(key), expected)
}

func assertNumericTag(span *mocktracer.Span, key string, expected float64) {
	switch value := span.Tag(key).(type) {
	case int:
		if float64(value) == expected {
			return
		}
	case int64:
		if float64(value) == expected {
			return
		}
	case float64:
		if value == expected {
			return
		}
	}
	panic(fmt.Sprintf("expected numeric tag %s=%v, got %v", key, expected, span.Tag(key)))
}

func assertTagCount(spans []*mocktracer.Span, key string, expected any, count int) {
	var actual int
	for _, span := range spans {
		if span.Tag(key) == expected {
			actual++
		}
	}
	assertEqual("tag count "+key, actual, count)
}

func assertEqual(label string, actual, expected any) {
	if actual != expected {
		panic(fmt.Sprintf("expected %s to be %v, got %v", label, expected, actual))
	}
}

type envSnapshot struct {
	key   string
	value string
	had   bool
}

func setEnv(values map[string]string) func() {
	snapshots := make([]envSnapshot, 0, len(values))
	for key, value := range values {
		old, had := env.Lookup(key)
		snapshots = append(snapshots, envSnapshot{key: key, value: old, had: had})
		_ = os.Setenv(key, value)
	}
	return func() {
		for i := len(snapshots) - 1; i >= 0; i-- {
			if snapshots[i].had {
				_ = os.Setenv(snapshots[i].key, snapshots[i].value)
			} else {
				_ = os.Unsetenv(snapshots[i].key)
			}
		}
	}
}

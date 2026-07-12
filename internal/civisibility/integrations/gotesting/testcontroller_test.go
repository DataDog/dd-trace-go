// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gotesting

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

var currentM *testing.M

var processRetryUnitTestPrefixes = []string{
	"TestRetryExecutionModeFromEnv",
	"TestDisableProcessRetryChildExecution",
	"TestProcessRetry",
	"TestRunProcessRetry",
	"TestBuildProcessRetry",
	"TestReadProcessRetry",
	"TestEffectiveProcessRetry",
	"TestCloseProcessRetry",
	"TestAttemptFromWaitError",
	"TestExecuteProcessRetry",
	"TestRunTestWithRetry",
	"TestWriteProcessRetry",
	"TestFinalizeProcessRetry",
	"TestCombineProcessRetry",
	"TestRecordProcessRetry",
}

var mTracer mocktracer.Tracer
var logsEntries []*mockedLogEntry
var parallelEfd bool

// TestMain is the entry point for testing and runs before any test.
func TestMain(m *testing.M) {
	// Enable logs collection for all test scenarios (propagates to spawned child processes).
	_ = os.Setenv("DD_CIVISIBILITY_LOGS_ENABLED", "true")

	const scenarioStarted = "**** [Scenario %s started] ****\n\n"
	// We need to spawn separated test process for each scenario
	scenarios := []string{"TestFlakyTestRetries", "TestEarlyFlakeDetection", "TestFlakyTestRetriesAndEarlyFlakeDetection", "TestIntelligentTestRunner", "TestManagementTests", "TestImpactedTests", "TestParallelEarlyFlakeDetection", "TestFlakyTestRetriesWithTransientSettingsFailure"}
	if coverageModeSupportsITRBackfill() {
		scenarios = append(scenarios, "TestIntelligentTestRunnerWithCoverageBackfill")
	}

	if internal.BoolEnv(scenarios[0], false) {
		fmt.Printf(scenarioStarted, scenarios[0])
		runFlakyTestRetriesTests(m)
	} else if internal.BoolEnv(scenarios[1], false) {
		fmt.Printf(scenarioStarted, scenarios[1])
		runEarlyFlakyTestDetectionTests(m)
	} else if internal.BoolEnv(scenarios[2], false) {
		fmt.Printf(scenarioStarted, scenarios[2])
		runFlakyTestRetriesWithEarlyFlakyTestDetectionTests(m, false)
	} else if internal.BoolEnv(scenarios[3], false) {
		fmt.Printf(scenarioStarted, scenarios[3])
		runIntelligentTestRunnerTests(m)
	} else if internal.BoolEnv(scenarios[4], false) {
		fmt.Printf(scenarioStarted, scenarios[4])
		runTestManagementTests(m)
	} else if internal.BoolEnv(scenarios[5], false) {
		fmt.Printf(scenarioStarted, scenarios[5])
		runFlakyTestRetriesWithEarlyFlakyTestDetectionTests(m, true)
	} else if internal.BoolEnv(scenarios[6], false) {
		fmt.Printf(scenarioStarted, scenarios[6])
		runParallelEarlyFlakyTestDetectionTests(m)
	} else if internal.BoolEnv(scenarios[7], false) {
		fmt.Printf(scenarioStarted, scenarios[7])
		runFlakyTestRetriesWithTransientSettingsFailureTests(m)
	} else if internal.BoolEnv("TestIntelligentTestRunnerWithCoverageBackfill", false) {
		fmt.Printf(scenarioStarted, "TestIntelligentTestRunnerWithCoverageBackfill")
		runIntelligentTestRunnerWithCoverageBackfillTests(m)
	} else if internal.BoolEnv("Bypass", false) {
		os.Exit(m.Run())
	} else {
		legacyScenarioRunFilter := "^(TestGetFieldPointerFrom|TestGetInternalTestArray|TestGetInternalBenchmarkArray|TestCommonPrivateFields_AddLevel|TestGetBenchmarkPrivateFields|TestTestifyLikeTest|TestMyTest01|TestMyTest02|Test_Foo|TestSkip|TestParallelSubTests|TestRetryWithPanic|TestRetryWithFail|TestNormalPassingAfterRetryAlwaysFail|TestEarlyFlakeDetection)$"
		tests := getInternalTestArray(m)
		if tests == nil {
			panic("unable to enumerate process retry unit tests")
		}
		runTestControllerSubprocess("ProcessRetryUnitTests", buildProcessRetryUnitRunFilter(*tests), "Bypass=true")
		for _, v := range scenarios {
			runTestControllerSubprocess(v, legacyScenarioRunFilter, fmt.Sprintf("%s=true", v))
		}
	}

	os.Exit(0)
}

func runTestControllerSubprocess(name, runFilter, environment string) {
	cmd := exec.Command(os.Args[0], buildTestControllerSubprocessArgs(os.Args[1:], runFilter)...)
	var output bytes.Buffer
	if log.DebugEnabled() {
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	} else {
		cmd.Stdout = &output
		cmd.Stderr = &output
	}
	cmd.Env = append(cmd.Env, os.Environ()...)
	cmd.Env = append(cmd.Env, environment)
	fmt.Printf("\n**** [RUNNING SCENARIO: %s]\n", name)
	err := cmd.Run()
	fmt.Printf("\n**** [SCENARIO %s IS DONE]\n\n", name)
	if err == nil {
		return
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		fmt.Printf("\n===========================================\n**** [SCENARIO %s FAILED WITH EXIT CODE: %d]\n", name, exitErr.ExitCode())
		if !log.DebugEnabled() {
			fmt.Printf("**** [SCENARIO %s OUTPUT]\n===========================================\n\n%s\n", name, output.String())
		}
		os.Exit(exitErr.ExitCode())
	}
	fmt.Printf("cmd.Run: %v\n", err)
	os.Exit(1)
}

func buildProcessRetryUnitRunFilter(tests []testing.InternalTest) string {
	names := make([]string, 0, len(tests))
	for _, test := range tests {
		for _, prefix := range processRetryUnitTestPrefixes {
			if strings.HasPrefix(test.Name, prefix) {
				names = append(names, regexp.QuoteMeta(test.Name))
				break
			}
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "^$"
	}
	return "^(" + strings.Join(names, "|") + ")($|/)"
}

func coverageModeSupportsITRBackfill() bool {
	switch testing.CoverMode() {
	case "count", "atomic":
		return true
	default:
		return false
	}
}

func runFlakyTestRetriesTests(m *testing.M) {
	// mock the settings api to enable automatic test retries
	server := setUpHTTPServer(true, true, false, &net.KnownTestsResponseData{
		Tests: net.KnownTestsResponseDataModules{
			"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting": net.KnownTestsResponseDataSuites{
				"reflections_test.go": []string{
					"TestGetFieldPointerFrom",
					"TestGetInternalTestArray",
					"TestGetInternalBenchmarkArray",
					"TestCommonPrivateFields_AddLevel",
					"TestGetBenchmarkPrivateFields",
				},
			},
		},
	},
		false, nil,
		false, nil,
		false,
		nil)
	defer server.Close()

	// set a custom retry count
	os.Setenv(constants.CIVisibilityFlakyRetryCountEnvironmentVariable, "10")

	// initialize the mock tracer for doing assertions on the finished spans
	currentM = m
	mTracer = integrations.InitializeCIVisibilityMock()

	// execute the tests, we are expecting some tests to fail and check the assertion later
	exitCode := RunM(m)
	if exitCode != 0 {
		panic("expected the exit code to be 0. Got exit code: " + strconv.Itoa(exitCode))
	}

	// get all finished spans
	finishedSpans := mTracer.FinishedSpans()
	showResourcesNameFromSpans(finishedSpans)

	// 1 session span
	// 1 module span
	// 4 suite span (testing_test.go, testify_test.go, testify_test.go/MySuite and reflections_test.go)
	// 5 tests from reflections_test.go
	// 1 TestMyTest01
	// 1 TestMyTest02 + 2 subtests
	// 1 Test_Foo + 3 subtests
	// 1 TestSkip
	// 1 TestRetryWithPanic + 3 retry tests from testing_test.go
	// 1 TestRetryWithFail + 3 retry tests from testing_test.go
	// 1 TestNormalPassingAfterRetryAlwaysFail
	// 1 TestEarlyFlakeDetection
	// 3 tests from testify_test.go and testify_test.go/MySuite

	// check spans by resource name
	checkSpansByResourceName(finishedSpans, "github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting", 1)
	checkSpansByResourceName(finishedSpans, "reflections_test.go", 1)
	checkSpansByResourceName(finishedSpans, "testify_test.go", 1)
	checkSpansByResourceName(finishedSpans, "testify_test.go/MySuite", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest01", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest02", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest02/sub01", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest02/sub01/sub03", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo/yellow_should_return_color", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo/banana_should_return_fruit", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo/duck_should_return_animal", 1)

	st01 := getSpansWithResourceName(finishedSpans, "testing_test.go.TestParallelSubTests/parallel_subtest_1")[0]
	st02 := getSpansWithResourceName(finishedSpans, "testing_test.go.TestParallelSubTests/parallel_subtest_2")[0]
	st03 := getSpansWithResourceName(finishedSpans, "testing_test.go.TestParallelSubTests/parallel_subtest_3")[0]

	st01EndTime := st01.StartTime().Add(st01.Duration())
	st02EndTime := st02.StartTime().Add(st02.Duration())

	fmt.Println(st01.StartTime(), st01EndTime)
	fmt.Println(st02.StartTime(), st02EndTime)
	fmt.Println(st03.StartTime())

	if st01EndTime.Before(st02.StartTime()) {
		panic("parallel testing does not work as expected, span 'testing_test.go.TestParallelSubTests/parallel_subtest_1' ends before span 'testing_test.go.TestParallelSubTests/parallel_subtest_2' starts")
	}
	if st02EndTime.Before(st03.StartTime()) {
		panic("parallel testing does not work as expected, span 'testing_test.go.TestParallelSubTests/parallel_subtest_2' ends before span 'testing_test.go.TestParallelSubTests/parallel_subtest_3' starts")
	}

	checkSpansByResourceName(finishedSpans, "testing_test.go.TestSkip", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestRetryWithPanic", 4)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestRetryWithFail", 4)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestNormalPassingAfterRetryAlwaysFail", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestEarlyFlakeDetection", 1)
	checkSpansByResourceName(finishedSpans, "testify_test.go.TestTestifyLikeTest", 1)
	testifySub01 := checkSpansByResourceName(finishedSpans, "testify_test.go/MySuite.TestTestifyLikeTest/TestMySuite", 1)[0]
	checkSpansByResourceName(finishedSpans, "testify_test.go/MySuite.TestTestifyLikeTest/TestMySuite/sub01", 1)

	// check that testify span has the correct source file
	if !strings.HasSuffix(testifySub01.Tag("test.source.file").(string), "/testify_test.go") {
		panic("source file should be testify_test.go, got " + testifySub01.Tag("test.source.file").(string))
	}

	// check spans by tag
	checkSpansByTagName(finishedSpans, constants.TestIsRetry, 6)
	trrSpan := checkSpansByTagName(finishedSpans, constants.TestRetryReason, 6)[0]
	if trrSpan.Tag(constants.TestRetryReason) != "auto_test_retry" {
		panic(fmt.Sprintf("expected retry reason to be %s, got %s", "auto_test_retry", trrSpan.Tag(constants.TestRetryReason)))
	}

	// check the test is new tag
	checkSpansByTagName(finishedSpans, constants.TestIsNew, 26)

	// check test.final_status - should only be on final execution spans
	// Coverage notes:
	// - Pass case: tests that eventually pass after retries (TestRetryWithPanic, TestRetryWithFail)
	// - Skip case: single-execution skips (TestSkip) and disabled/quarantined tests (see TestManagementTests)
	// - Fail case: would require a test that always fails without being disabled/quarantined.
	//   The fail logic is covered by calculateFinalStatus() unit tests (anyFailed=true, anyPassed=false => fail).
	// - Slow EFD (>=5m) + flaky fallthrough: impractical to test due to 5-minute test duration requirement.
	//   The logic is covered by computeAdjustedRetryCount() which returns 0 for tests >= 5 minutes.
	//
	// TestRetryWithPanic has 4 executions (1 original + 3 retries), passes on 4th -> final_status=pass on last execution only
	testRetryWithPanicSpans := checkSpansByResourceName(finishedSpans, "testing_test.go.TestRetryWithPanic", 4)
	checkSpansByTagValue(testRetryWithPanicSpans, constants.TestFinalStatus, constants.TestStatusPass, 1)

	// TestRetryWithFail has 4 executions, passes on 4th -> final_status=pass on last execution only
	testRetryWithFailSpans := checkSpansByResourceName(finishedSpans, "testing_test.go.TestRetryWithFail", 4)
	checkSpansByTagValue(testRetryWithFailSpans, constants.TestFinalStatus, constants.TestStatusPass, 1)

	// TestMyTest01 is a single execution pass -> final_status=pass
	testMyTest01Spans := checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest01", 1)
	checkSpansByTagValue(testMyTest01Spans, constants.TestFinalStatus, constants.TestStatusPass, 1)

	// TestSkip is a single execution skip -> final_status=skip
	testSkipSpans := checkSpansByResourceName(finishedSpans, "testing_test.go.TestSkip", 1)
	checkSpansByTagValue(testSkipSpans, constants.TestFinalStatus, constants.TestStatusSkip, 1)

	// TestNormalPassingAfterRetryAlwaysFail is a single execution pass -> final_status=pass
	testNormalPassingSpans := checkSpansByResourceName(finishedSpans, "testing_test.go.TestNormalPassingAfterRetryAlwaysFail", 1)
	checkSpansByTagValue(testNormalPassingSpans, constants.TestFinalStatus, constants.TestStatusPass, 1)

	// check if suite has both test code owners and source file tags
	suiteSpans := getSpansWithType(finishedSpans, constants.SpanTypeTestSuite)
	checkSpansByTagName(suiteSpans, constants.TestCodeOwners, 4)
	checkSpansByTagName(suiteSpans, constants.TestSourceFile, 4)

	// check spans by type
	checkSpansByType(finishedSpans,
		32,
		1,
		1,
		4,
		31,
		0)

	// check capabilities tags
	checkCapabilitiesTags(finishedSpans)

	// check logs
	checkLogs()

	os.Exit(0)
}

// runFlakyTestRetriesWithTransientSettingsFailureTests reproduces the root cause of the flaky
// TestRetryWithPanic crash: a transient failure on the settings fetch.
//
// It stands up the same backend as the flaky-test-retries scenario but routes the tracer through
// a fault-injecting proxy that, on the first settings request, returns a 200 OK response claiming
// gzip encoding while carrying a body that cannot be decompressed. This is exactly the transient
// body/decompress failure that the fetch hardening now retries.
//
// With the hardening in place the client retries the settings call, the flaky-retry wrapper is
// reliably installed, and the synthetic panic/fail tests run wrapped (4 executions each) so the
// process exits cleanly. Without it the first failure permanently disables every feature, the
// synthetic tests run unwrapped, and TestRetryWithPanic crashes this subprocess — which the parent
// TestMain observes as a non-zero exit code.
func runFlakyTestRetriesWithTransientSettingsFailureTests(m *testing.M) {
	// Backend mock identical to the standard flaky-test-retries scenario.
	backend := setUpHTTPServer(true, true, false, &net.KnownTestsResponseData{
		Tests: net.KnownTestsResponseDataModules{
			"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting": net.KnownTestsResponseDataSuites{
				"reflections_test.go": []string{
					"TestGetFieldPointerFrom",
					"TestGetInternalTestArray",
					"TestGetInternalBenchmarkArray",
					"TestCommonPrivateFields_AddLevel",
					"TestGetBenchmarkPrivateFields",
				},
			},
		},
	},
		false, nil,
		false, nil,
		false,
		nil)
	defer backend.Close()

	backendURL, err := url.Parse(backend.URL)
	if err != nil {
		panic(fmt.Sprintf("failed to parse backend url: %s", err))
	}
	proxy := httputil.NewSingleHostReverseProxy(backendURL)

	// Fail the first settings request with an undecodable gzip body, then proxy everything
	// (including the retry) to the real backend.
	var settingsFailed atomic.Bool
	faulty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/libraries/tests/services/setting" && settingsFailed.CompareAndSwap(false, true) {
			log.Debug("MockApi injecting transient settings failure (undecodable gzip body)")
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set(net.HeaderContentEncoding, net.ContentEncodingGzip)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("this is not valid gzip data"))
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	defer faulty.Close()

	// Point the tracer at the fault-injecting proxy instead of the backend.
	os.Setenv(constants.CIVisibilityAgentlessURLEnvironmentVariable, faulty.URL)

	// set a custom retry count
	os.Setenv(constants.CIVisibilityFlakyRetryCountEnvironmentVariable, "10")

	// initialize the mock tracer for doing assertions on the finished spans
	currentM = m
	mTracer = integrations.InitializeCIVisibilityMock()

	// execute the tests; the suite must not crash even though the settings fetch failed once
	exitCode := RunM(m)
	if exitCode != 0 {
		panic("expected the exit code to be 0. Got exit code: " + strconv.Itoa(exitCode))
	}

	// the transient failure must actually have been exercised
	if !settingsFailed.Load() {
		panic("expected the settings endpoint to have been called at least once")
	}

	// get all finished spans
	finishedSpans := mTracer.FinishedSpans()
	showResourcesNameFromSpans(finishedSpans)

	// The settings fetch was retried, so the flaky-retry wrapper owns the synthetic tests: they
	// panic/fail on the first executions and pass after the auto retries, producing 4 executions
	// each. This proves the wrapper was reliably installed despite the transient failure.
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestRetryWithPanic", 4)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestRetryWithFail", 4)
	checkSpansByTagName(finishedSpans, constants.TestIsRetry, 6)

	os.Exit(0)
}

func buildTestControllerSubprocessArgs(originalArgs []string, runFilter string) []string {
	preserved := make([]string, 0, len(originalArgs)+1)
	boundary := []string(nil)
	for i := 0; i < len(originalArgs); i++ {
		arg := originalArgs[i]
		if arg == "--" || !processRetryIsFlagToken(arg) {
			boundary = append(boundary, originalArgs[i:]...)
			break
		}
		name, _, hasValue := processRetrySplitFlag(arg)
		if name == "-test.run" || name == "-run" {
			if !hasValue && i+1 < len(originalArgs) {
				i++
			}
			continue
		}
		preserved = append(preserved, arg)
		if hasValue {
			continue
		}
		registered := flag.CommandLine.Lookup(strings.TrimPrefix(name, "-"))
		if registered == nil {
			preserved = preserved[:len(preserved)-1]
			boundary = append(boundary, originalArgs[i:]...)
			break
		}
		if boolFlag, ok := registered.Value.(processRetryBoolFlag); ok && boolFlag.IsBoolFlag() {
			continue
		}
		if i+1 < len(originalArgs) {
			i++
			preserved = append(preserved, originalArgs[i])
		}
	}
	args := make([]string, 0, len(preserved)+1+len(boundary))
	args = append(args, preserved...)
	args = append(args, "-test.run="+runFilter)
	args = append(args, boundary...)
	return args
}

func runEarlyFlakyTestDetectionTests(m *testing.M) {
	// mock the settings api to enable automatic test retries
	server := setUpHTTPServer(false, true, true, &net.KnownTestsResponseData{
		Tests: net.KnownTestsResponseDataModules{
			"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting": net.KnownTestsResponseDataSuites{
				"reflections_test.go": []string{
					"TestGetFieldPointerFrom",
					"TestGetInternalTestArray",
					"TestGetInternalBenchmarkArray",
					"TestCommonPrivateFields_AddLevel",
					"TestGetBenchmarkPrivateFields",
				},
			},
		},
	},
		false, nil,
		false, nil,
		false,
		nil)
	defer server.Close()

	// initialize the mock tracer for doing assertions on the finished spans
	currentM = m
	mTracer = integrations.InitializeCIVisibilityMock()

	// execute the tests, we are expecting some tests to fail and check the assertion later
	exitCode := RunM(m)
	if exitCode != 0 {
		panic("expected the exit code to be 0. Got exit code: " + strconv.Itoa(exitCode))
	}

	// get all finished spans
	finishedSpans := mTracer.FinishedSpans()
	showResourcesNameFromSpans(finishedSpans)

	// 1 session span
	// 1 module span
	// 4 suite span (testing_test.go, testify_test.go, testify_test.go/MySuite and reflections_test.go)
	// 5 tests from reflections_test.go
	// 11 TestMyTest01
	// 11 TestMyTest02 + 22 subtests
	// 11 Test_Foo + 33 subtests
	// 1 TestSkip
	// 11 TestRetryWithPanic
	// 11 TestRetryWithFail
	// 11 TestNormalPassingAfterRetryAlwaysFail
	// 11 TestEarlyFlakeDetection
	// 22 normal spans from testing_test.go
	// 33 tests from testify_test.go and testify_test.go/MySuite

	// check spans by resource name
	checkSpansByResourceName(finishedSpans, "github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting", 1)
	checkSpansByResourceName(finishedSpans, "reflections_test.go", 1)
	checkSpansByResourceName(finishedSpans, "testify_test.go", 1)
	checkSpansByResourceName(finishedSpans, "testify_test.go/MySuite", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest01", 11)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest02", 11)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest02/sub01", 11)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest02/sub01/sub03", 11)
	checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo", 11)
	checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo/yellow_should_return_color", 11)
	checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo/banana_should_return_fruit", 11)
	checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo/duck_should_return_animal", 11)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestSkip", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestRetryWithPanic", 11)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestRetryWithFail", 11)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestNormalPassingAfterRetryAlwaysFail", 11)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestEarlyFlakeDetection", 11)
	checkSpansByResourceName(finishedSpans, "testify_test.go.TestTestifyLikeTest", 11)
	testifySub01 := checkSpansByResourceName(finishedSpans, "testify_test.go/MySuite.TestTestifyLikeTest/TestMySuite", 11)[0]
	checkSpansByResourceName(finishedSpans, "testify_test.go/MySuite.TestTestifyLikeTest/TestMySuite/sub01", 11)

	// check that testify span has the correct source file
	if !strings.HasSuffix(testifySub01.Tag("test.source.file").(string), "/testify_test.go") {
		panic("source file should be testify_test.go, got " + testifySub01.Tag("test.source.file").(string))
	}

	// check spans by tag
	checkSpansByTagName(finishedSpans, constants.TestIsNew, 210)
	checkSpansByTagName(finishedSpans, constants.TestIsRetry, 190)
	trrSpan := checkSpansByTagName(finishedSpans, constants.TestRetryReason, 190)[0]
	if trrSpan.Tag(constants.TestRetryReason) != "early_flake_detection" {
		panic(fmt.Sprintf("expected retry reason to be %s, got %s", "early_flake_detection", trrSpan.Tag(constants.TestRetryReason)))
	}

	// check test.final_status - most EFD tests run 11 executions (1 original + 10 retries), final_status only on the last
	// Clean skips are terminal and report final_status on their single execution.
	// TestMyTest01 passes all 11 times -> final_status=pass (only on last execution)
	efdTestMyTest01Spans := checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest01", 11)
	checkSpansByTagValue(efdTestMyTest01Spans, constants.TestFinalStatus, constants.TestStatusPass, 1)

	// TestSkip clean-skips once and does not schedule EFD retries -> final_status=skip
	efdTestSkipSpans := checkSpansByResourceName(finishedSpans, "testing_test.go.TestSkip", 1)
	checkSpansByTagValue(efdTestSkipSpans, constants.TestStatus, constants.TestStatusSkip, 1)
	checkSpansByTagValue(efdTestSkipSpans, constants.TestFinalStatus, constants.TestStatusSkip, 1)
	checkSpansByTagName(efdTestSkipSpans, constants.TestRetryReason, 0)
	checkSpansByTagName(efdTestSkipSpans, constants.TestIsRetry, 0)
	checkSpansByTagValue(efdTestSkipSpans, constants.TestSkipReason, "Nothing to do here, skipping!", 1)

	// TestRetryWithPanic fails first 3 times, passes 4-11 -> anyPassed=true -> final_status=pass (only on last execution)
	efdTestRetryWithPanicSpans := checkSpansByResourceName(finishedSpans, "testing_test.go.TestRetryWithPanic", 11)
	checkSpansByTagValue(efdTestRetryWithPanicSpans, constants.TestFinalStatus, constants.TestStatusPass, 1)

	// TestRetryWithFail fails first 3 times, passes 4-11 -> anyPassed=true -> final_status=pass (only on last execution)
	efdTestRetryWithFailSpans := checkSpansByResourceName(finishedSpans, "testing_test.go.TestRetryWithFail", 11)
	checkSpansByTagValue(efdTestRetryWithFailSpans, constants.TestFinalStatus, constants.TestStatusPass, 1)

	// TestNormalPassingAfterRetryAlwaysFail passes all 11 times -> final_status=pass (only on last execution)
	efdTestNormalPassingSpans := checkSpansByResourceName(finishedSpans, "testing_test.go.TestNormalPassingAfterRetryAlwaysFail", 11)
	checkSpansByTagValue(efdTestNormalPassingSpans, constants.TestFinalStatus, constants.TestStatusPass, 1)

	// check if suite has both test code owners and source file tags
	suiteSpans := getSpansWithType(finishedSpans, constants.SpanTypeTestSuite)
	checkSpansByTagName(suiteSpans, constants.TestCodeOwners, 4)
	checkSpansByTagName(suiteSpans, constants.TestSourceFile, 4)

	// check spans by type
	checkSpansByType(finishedSpans,
		152,
		1,
		1,
		4,
		215,
		0)

	// check capabilities tags
	checkCapabilitiesTags(finishedSpans)

	// check logs
	checkLogs()

	os.Exit(0)
}

func runParallelEarlyFlakyTestDetectionTests(m *testing.M) {
	// mock the settings api to enable automatic test retries
	server := setUpHTTPServer(false, true, true, &net.KnownTestsResponseData{
		Tests: net.KnownTestsResponseDataModules{
			"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting": net.KnownTestsResponseDataSuites{
				"reflections_test.go": []string{
					"TestGetFieldPointerFrom",
					"TestGetInternalTestArray",
					"TestGetInternalBenchmarkArray",
					"TestCommonPrivateFields_AddLevel",
					"TestGetBenchmarkPrivateFields",
				},
			},
		},
	},
		false, nil,
		false, nil,
		false,
		nil)
	defer server.Close()

	// set a custom retry count
	os.Setenv(constants.CIVisibilityInternalParallelEarlyFlakeDetectionEnabled, "true")
	parallelEfd = true

	// initialize the mock tracer for doing assertions on the finished spans
	currentM = m
	mTracer = integrations.InitializeCIVisibilityMock()

	// execute the tests, we are expecting some tests to fail and check the assertion later
	exitCode := RunM(m)
	if exitCode != 0 {
		panic("expected the exit code to be 0. Got exit code: " + strconv.Itoa(exitCode))
	}

	// get all finished spans
	finishedSpans := mTracer.FinishedSpans()
	showResourcesNameFromSpans(finishedSpans)

	// 1 session span
	// 1 module span
	// 4 suite span (testing_test.go, testify_test.go, testify_test.go/MySuite and reflections_test.go)
	// 5 tests from reflections_test.go
	// 1 TestMyTest01
	// 1 TestMyTest02 + 22 subtests
	// 1 Test_Foo + 33 subtests
	// 1 TestSkip
	// 1 TestRetryWithPanic
	// 1 TestRetryWithFail
	// 1 TestNormalPassingAfterRetryAlwaysFail
	// 11 TestEarlyFlakeDetection
	// 2 normal spans from testing_test.go
	// 1 clean skipped test from testify_test.go
	// 2 normal spans from testify_test.go and testify_test.go/MySuite

	// check spans by resource name
	checkSpansByResourceName(finishedSpans, "github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting", 1)
	checkSpansByResourceName(finishedSpans, "reflections_test.go", 1)
	checkSpansByResourceName(finishedSpans, "testify_test.go", 1)
	checkSpansByResourceName(finishedSpans, "testify_test.go/MySuite", 0)
	checkSpansByResourceName(finishedSpans, "testing_test.go", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest01", 11)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest02", 11)
	checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo", 11)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestSkip", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestRetryWithPanic", 11)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestRetryWithFail", 11)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestNormalPassingAfterRetryAlwaysFail", 11)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestEarlyFlakeDetection", 11)
	checkSpansByResourceName(finishedSpans, "testify_test.go.TestTestifyLikeTest", 1)

	// check spans by tag
	checkSpansByTagName(finishedSpans, constants.TestIsNew, 178)
	checkSpansByTagName(finishedSpans, constants.TestIsRetry, 160)
	trrSpan := checkSpansByTagName(finishedSpans, constants.TestRetryReason, 160)[0]
	if trrSpan.Tag(constants.TestRetryReason) != "early_flake_detection" {
		panic(fmt.Sprintf("expected retry reason to be %s, got %s", "early_flake_detection", trrSpan.Tag(constants.TestRetryReason)))
	}

	// check test.final_status - parallel EFD still skips final_status for fanned-out
	// EFD attempts. A clean skip stops before fan-out, so it keeps final_status=skip.
	// The 5 known tests from reflections_test.go (single execution, no EFD) have final_status=pass.
	checkSpansByTagName(finishedSpans, constants.TestFinalStatus, 7)
	checkSpansByTagValue(finishedSpans, constants.TestFinalStatus, constants.TestStatusPass, 5)
	checkSpansByTagValue(finishedSpans, constants.TestFinalStatus, constants.TestStatusSkip, 2)
	parallelEFDTestSkipSpans := checkSpansByResourceName(finishedSpans, "testing_test.go.TestSkip", 1)
	checkSpansByTagValue(parallelEFDTestSkipSpans, constants.TestStatus, constants.TestStatusSkip, 1)
	checkSpansByTagValue(parallelEFDTestSkipSpans, constants.TestFinalStatus, constants.TestStatusSkip, 1)
	checkSpansByTagName(parallelEFDTestSkipSpans, constants.TestRetryReason, 0)
	checkSpansByTagName(parallelEFDTestSkipSpans, constants.TestIsRetry, 0)
	checkSpansByTagValue(parallelEFDTestSkipSpans, constants.TestSkipReason, "Nothing to do here, skipping!", 1)
	parallelEFDTestifySkipSpans := checkSpansByResourceName(finishedSpans, "testify_test.go.TestTestifyLikeTest", 1)
	checkSpansByTagValue(parallelEFDTestifySkipSpans, constants.TestStatus, constants.TestStatusSkip, 1)
	checkSpansByTagValue(parallelEFDTestifySkipSpans, constants.TestFinalStatus, constants.TestStatusSkip, 1)
	checkSpansByTagName(parallelEFDTestifySkipSpans, constants.TestRetryReason, 0)
	checkSpansByTagName(parallelEFDTestifySkipSpans, constants.TestIsRetry, 0)
	checkSpansByTagName(parallelEFDTestifySkipSpans, constants.TestSkipReason, 0)

	// check capabilities tags
	checkCapabilitiesTags(finishedSpans)

	// check logs
	checkLogs()

	os.Exit(0)
}

func runFlakyTestRetriesWithEarlyFlakyTestDetectionTests(m *testing.M, impactedTests bool) {
	// mock the settings api to enable automatic test retries
	server := setUpHTTPServer(true, true, true, &net.KnownTestsResponseData{
		Tests: net.KnownTestsResponseDataModules{
			"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting": net.KnownTestsResponseDataSuites{
				"reflections_test.go": []string{
					"TestGetFieldPointerFrom",
					"TestGetInternalTestArray",
					"TestGetInternalBenchmarkArray",
					"TestCommonPrivateFields_AddLevel",
					"TestGetBenchmarkPrivateFields",
				},
				"testing_test.go": []string{
					"TestMyTest01",
					"TestMyTest02",
					"Test_Foo",
					"TestWithExternalCalls",
					"TestSkip",
					"TestRetryWithPanic",
					"TestRetryWithFail",
					"TestRetryAlwaysFail",
					"TestNormalPassingAfterRetryAlwaysFail",
				},
				"testify_test.go": []string{
					"TestTestifyLikeTest",
				},
				"testify_test.go/MySuite": []string{
					"TestTestifyLikeTest/TestMySuite",
					"TestTestifyLikeTest/TestMySuite/sub01",
				},
			},
		},
	},
		false, nil,
		false, nil,
		impactedTests,
		nil)
	defer server.Close()

	// set a custom retry count
	os.Setenv(constants.CIVisibilityFlakyRetryCountEnvironmentVariable, "10")

	// set impacted tests variables
	if impactedTests {
		// set the commit sha to a known value to always have the same git diff
		base := "b97e7cbb464aef26da8cb5c07a225f7a144f26a4"
		head := "3808532bc719ca418b938afb680246109768f343"
		// 3808532bc719ca418b938afb680246109768f343 (feat) internal/civisibility: impacted tests (#3389)
		// ...
		// b97e7cbb464aef26da8cb5c07a225f7a144f26a4 v2.0.0 (#2427)

		// let's make sure we have both shas available
		_ = exec.Command("git", "fetch", "origin", base).Run()
		_ = exec.Command("git", "fetch", "origin", head).Run()

		utils.AddCITags(constants.GitPrBaseCommit, base)
		utils.AddCITags(constants.GitHeadCommit, head)
	}

	// initialize the mock tracer for doing assertions on the finished spans
	currentM = m
	mTracer = integrations.InitializeCIVisibilityMock()

	// execute the tests, we are expecting some tests to fail and check the assertion later
	exitCode := RunM(m)
	if exitCode != 0 {
		panic("expected the exit code to be 0. Got exit code: " + strconv.Itoa(exitCode))
	}

	// get all finished spans
	finishedSpans := mTracer.FinishedSpans()
	showResourcesNameFromSpans(finishedSpans)

	// 1 session span
	// 1 module span
	// 4 suite span (testing_test.go, testify_test.go, testify_test.go/MySuite and reflections_test.go)
	// 5 tests from reflections_test.go
	// 1 TestMyTest01
	// 1 TestMyTest02 + 2 subtests
	// 1 Test_Foo + 3 subtests
	// 1 TestWithExternalCalls + 2 subtests
	// 1 TestSkip
	// 1 TestRetryWithPanic + 3 retry tests from testing_test.go
	// 1 TestRetryWithFail + 3 retry tests from testing_test.go
	// 1 TestNormalPassingAfterRetryAlwaysFail
	// 1 TestEarlyFlakeDetection + 10 EFD retries
	// 2 normal spans from testing_test.go
	// 3 tests from testify_test.go and testify_test.go/MySuite

	// check spans by resource name
	checkSpansByResourceName(finishedSpans, "github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting", 1)
	checkSpansByResourceName(finishedSpans, "reflections_test.go", 1)
	checkSpansByResourceName(finishedSpans, "testify_test.go", 1)
	checkSpansByResourceName(finishedSpans, "testify_test.go/MySuite", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest01", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest02", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest02/sub01", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest02/sub01/sub03", 1)
	if impactedTests {
		// impacteds tests will trigger EFD retries (if the test is not quarantined nor disabled)
		checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo", 11)
		checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo/yellow_should_return_color", 11)
		checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo/banana_should_return_fruit", 11)
		checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo/duck_should_return_animal", 11)
		checkSpansByResourceName(finishedSpans, "testing_test.go.TestSkip", 1)
		checkSpansByResourceName(finishedSpans, "testing_test.go.TestRetryWithPanic", 4)
		checkSpansByResourceName(finishedSpans, "testing_test.go.TestRetryWithFail", 4)

	} else {
		checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo", 1)
		checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo/yellow_should_return_color", 1)
		checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo/banana_should_return_fruit", 1)
		checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo/duck_should_return_animal", 1)
		checkSpansByResourceName(finishedSpans, "testing_test.go.TestSkip", 1)
		checkSpansByResourceName(finishedSpans, "testing_test.go.TestRetryWithPanic", 4)
		checkSpansByResourceName(finishedSpans, "testing_test.go.TestRetryWithFail", 4)
	}
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestNormalPassingAfterRetryAlwaysFail", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestEarlyFlakeDetection", 11)
	checkSpansByResourceName(finishedSpans, "testify_test.go.TestTestifyLikeTest", 1)
	testifySub01 := checkSpansByResourceName(finishedSpans, "testify_test.go/MySuite.TestTestifyLikeTest/TestMySuite", 1)[0]
	checkSpansByResourceName(finishedSpans, "testify_test.go/MySuite.TestTestifyLikeTest/TestMySuite/sub01", 1)

	// check that testify span has the correct source file
	if !strings.HasSuffix(testifySub01.Tag("test.source.file").(string), "/testify_test.go") {
		panic("source file should be testify_test.go, got " + testifySub01.Tag("test.source.file").(string))
	}

	// check capabilities tags
	checkCapabilitiesTags(finishedSpans)

	// check spans by tag
	checkSpansByTagName(finishedSpans, constants.TestIsNew, 55)

	// check if suite has both test code owners and source file tags
	suiteSpans := getSpansWithType(finishedSpans, constants.SpanTypeTestSuite)
	checkSpansByTagName(suiteSpans, constants.TestCodeOwners, 4)
	checkSpansByTagName(suiteSpans, constants.TestSourceFile, 4)

	// Impacted tests
	if impactedTests {
		checkSpansByTagName(finishedSpans, constants.TestIsRetry, 96)

		// check spans by type
		checkSpansByType(finishedSpans,
			97,
			1,
			1,
			4,
			121,
			0)

		checkSpansByTagName(finishedSpans, constants.TestIsModified, 33)
	} else {
		checkSpansByTagName(finishedSpans, constants.TestIsRetry, 56)

		// check spans by type
		checkSpansByType(finishedSpans,
			38,
			1,
			1,
			4,
			81,
			0)

		checkSpansByTagName(finishedSpans, constants.TestIsModified, 0)
	}

	// check logs
	checkLogs()

	os.Exit(0)
}

func runIntelligentTestRunnerTests(m *testing.M) {
	// mock the settings api to enable automatic test retries
	server := setUpHTTPServer(true, true, false, nil, true, []net.SkippableResponseDataAttributes{
		{
			Suite: "testing_test.go",
			Name:  "TestMyTest01",
		},
		{
			Suite: "testing_test.go",
			Name:  "TestMyTest02",
		},
		{
			Suite: "testing_test.go",
			Name:  "Test_Foo",
		},
		{
			Suite: "testing_test.go",
			Name:  "TestRetryWithPanic",
		},
		{
			Suite: "testing_test.go",
			Name:  "TestRetryWithFail",
		},
		{
			Suite: "testing_test.go",
			Name:  "TestRetryAlwaysFail",
		},
		{
			Suite: "testing_test.go",
			Name:  "TestNormalPassingAfterRetryAlwaysFail",
		},
	},
		false, nil,
		false,
		nil)
	defer server.Close()

	// initialize the mock tracer for doing assertions on the finished spans
	currentM = m
	mTracer = integrations.InitializeCIVisibilityMock()

	// execute the tests, we are expecting some tests to fail and check the assertion later
	exitCode := RunM(m)
	if exitCode != 0 {
		panic("expected the exit code to be 0. All tests should pass (failed ones should be skipped by ITR).")
	}

	// get all finished spans
	finishedSpans := mTracer.FinishedSpans()
	showResourcesNameFromSpans(finishedSpans)

	if testing.CoverMode() != "" {
		checkIntelligentTestRunnerWithMissingBackendCoverage(finishedSpans)
		os.Exit(0)
	}

	// 1 session span
	// 1 module span
	// 4 suite span (testing_test.go, testify_test.go, testify_test.go/MySuite and reflections_test.go)
	// 5 tests from reflections_test.go
	// 1 TestMyTest01
	// 1 TestMyTest02
	// 1 Test_Foo
	// 1 TestSkip
	// 1 TestRetryWithPanic
	// 1 TestRetryWithFail
	// 1 TestRetryAlwaysFail
	// 1 TestNormalPassingAfterRetryAlwaysFail
	// 1 TestEarlyFlakeDetection
	// 3 tests from testify_test.go and testify_test.go/MySuite

	// check spans by resource name
	checkSpansByResourceName(finishedSpans, "github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting", 1)
	checkSpansByResourceName(finishedSpans, "reflections_test.go", 1)
	checkSpansByResourceName(finishedSpans, "testify_test.go", 1)
	checkSpansByResourceName(finishedSpans, "testify_test.go/MySuite", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go", 1)
	checkSpansByResourceName(finishedSpans, "reflections_test.go.TestGetFieldPointerFrom", 1)
	checkSpansByResourceName(finishedSpans, "reflections_test.go.TestGetInternalTestArray", 1)
	checkSpansByResourceName(finishedSpans, "reflections_test.go.TestGetInternalBenchmarkArray", 1)
	checkSpansByResourceName(finishedSpans, "reflections_test.go.TestCommonPrivateFields_AddLevel", 1)
	checkSpansByResourceName(finishedSpans, "reflections_test.go.TestGetBenchmarkPrivateFields", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest01", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest02", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest02/sub01", 0)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest02/sub01/sub03", 0)
	checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo/yellow_should_return_color", 0)
	checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo/banana_should_return_fruit", 0)
	checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo/duck_should_return_animal", 0)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestSkip", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestRetryWithPanic", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestRetryWithFail", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestNormalPassingAfterRetryAlwaysFail", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestEarlyFlakeDetection", 1)
	checkSpansByResourceName(finishedSpans, "testify_test.go.TestTestifyLikeTest", 1)
	testifySub01 := checkSpansByResourceName(finishedSpans, "testify_test.go/MySuite.TestTestifyLikeTest/TestMySuite", 1)[0]
	checkSpansByResourceName(finishedSpans, "testify_test.go/MySuite.TestTestifyLikeTest/TestMySuite/sub01", 1)

	// check that testify span has the correct source file
	if !strings.HasSuffix(testifySub01.Tag("test.source.file").(string), "/testify_test.go") {
		panic("source file should be testify_test.go, got " + testifySub01.Tag("test.source.file").(string))
	}

	checkIntelligentTestRunnerSkipTags(finishedSpans)

	// check if suite has both test code owners and source file tags
	suiteSpans := getSpansWithType(finishedSpans, constants.SpanTypeTestSuite)
	checkSpansByTagName(suiteSpans, constants.TestCodeOwners, 4)
	checkSpansByTagName(suiteSpans, constants.TestSourceFile, 4)

	// check spans by type
	checkSpansByType(finishedSpans,
		17,
		1,
		1,
		4,
		20,
		0)

	// check capabilities tags
	checkCapabilitiesTags(finishedSpans)

	sessionSpans := getSpansWithType(finishedSpans, constants.SpanTypeTestSession)
	if len(sessionSpans) != 1 {
		panic(fmt.Sprintf("expected exactly one session span, got %d", len(sessionSpans)))
	}
	if got := sessionSpans[0].Tag(constants.CodeCoverageEnabled); got != "false" {
		panic(fmt.Sprintf("expected %s=false when ITR is enabled without coverage, got %v", constants.CodeCoverageEnabled, got))
	}
	checkITRTestsSkippingEnabledTag(finishedSpans, "true")

	fmt.Println("All tests passed.")
	os.Exit(0)
}

func runIntelligentTestRunnerWithCoverageBackfillTests(m *testing.M) {
	backendCoverage := map[string][]byte{
		"internal/civisibility/integrations/gotesting/testing.go": {0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
	}
	server := setUpHTTPServer(true, true, false, nil, true, []net.SkippableResponseDataAttributes{
		{
			Suite: "testing_test.go",
			Name:  "TestMyTest01",
		},
		{
			Suite: "testing_test.go",
			Name:  "TestMyTest02",
		},
		{
			Suite: "testing_test.go",
			Name:  "Test_Foo",
		},
		{
			Suite: "testing_test.go",
			Name:  "TestRetryWithPanic",
		},
		{
			Suite: "testing_test.go",
			Name:  "TestRetryWithFail",
		},
		{
			Suite: "testing_test.go",
			Name:  "TestNormalPassingAfterRetryAlwaysFail",
		},
	},
		false, nil,
		false,
		backendCoverage)
	defer server.Close()

	currentM = m
	mTracer = integrations.InitializeCIVisibilityMock()

	exitCode := RunM(m)
	if exitCode != 0 {
		panic("expected the exit code to be 0. All tests should pass (failed ones should be skipped by ITR).")
	}

	finishedSpans := mTracer.FinishedSpans()
	showResourcesNameFromSpans(finishedSpans)
	checkIntelligentTestRunnerWithCoverageBackfill(finishedSpans)

	fmt.Println("All tests passed.")
	os.Exit(0)
}

func checkIntelligentTestRunnerWithMissingBackendCoverage(finishedSpans []*mocktracer.Span) {
	// Missing aggregate backend coverage disables coverage backfill only. ITR
	// skipping still follows the backend skippable-test list, matching the Java
	// reference implementation.
	checkSpansByResourceName(finishedSpans, "github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting", 1)
	checkSpansByResourceName(finishedSpans, "reflections_test.go", 1)
	checkSpansByResourceName(finishedSpans, "testify_test.go", 1)
	checkSpansByResourceName(finishedSpans, "testify_test.go/MySuite", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go", 1)
	checkSpansByResourceName(finishedSpans, "reflections_test.go.TestGetFieldPointerFrom", 1)
	checkSpansByResourceName(finishedSpans, "reflections_test.go.TestGetInternalTestArray", 1)
	checkSpansByResourceName(finishedSpans, "reflections_test.go.TestGetInternalBenchmarkArray", 1)
	checkSpansByResourceName(finishedSpans, "reflections_test.go.TestCommonPrivateFields_AddLevel", 1)
	checkSpansByResourceName(finishedSpans, "reflections_test.go.TestGetBenchmarkPrivateFields", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest01", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest02", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest02/sub01", 0)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest02/sub01/sub03", 0)
	checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo/yellow_should_return_color", 0)
	checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo/banana_should_return_fruit", 0)
	checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo/duck_should_return_animal", 0)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestSkip", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestRetryWithPanic", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestRetryWithFail", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestNormalPassingAfterRetryAlwaysFail", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestEarlyFlakeDetection", 1)
	checkSpansByResourceName(finishedSpans, "testify_test.go.TestTestifyLikeTest", 1)
	testifySub01 := checkSpansByResourceName(finishedSpans, "testify_test.go/MySuite.TestTestifyLikeTest/TestMySuite", 1)[0]
	checkSpansByResourceName(finishedSpans, "testify_test.go/MySuite.TestTestifyLikeTest/TestMySuite/sub01", 1)

	if !strings.HasSuffix(testifySub01.Tag("test.source.file").(string), "/testify_test.go") {
		panic("source file should be testify_test.go, got " + testifySub01.Tag("test.source.file").(string))
	}

	checkSpansByTagName(finishedSpans, constants.TestIsNew, 0)
	checkIntelligentTestRunnerSkipTags(finishedSpans)

	suiteSpans := getSpansWithType(finishedSpans, constants.SpanTypeTestSuite)
	checkSpansByTagName(suiteSpans, constants.TestCodeOwners, 4)
	checkSpansByTagName(suiteSpans, constants.TestSourceFile, 4)

	checkSpansByType(finishedSpans,
		17,
		1,
		1,
		4,
		20,
		0)

	checkCapabilitiesTags(finishedSpans)

	sessionSpans := getSpansWithType(finishedSpans, constants.SpanTypeTestSession)
	if len(sessionSpans) != 1 {
		panic(fmt.Sprintf("expected exactly one session span, got %d", len(sessionSpans)))
	}
	if got := sessionSpans[0].Tag(constants.CodeCoverageEnabled); got != "false" {
		panic(fmt.Sprintf("expected %s=false when ITR is enabled without backend coverage, got %v", constants.CodeCoverageEnabled, got))
	}
	checkITRTestsSkippingEnabledTag(finishedSpans, "true")

	checkLogs()
}

func checkIntelligentTestRunnerWithCoverageBackfill(finishedSpans []*mocktracer.Span) {
	checkIntelligentTestRunnerSkipTags(finishedSpans)

	sessionSpans := getSpansWithType(finishedSpans, constants.SpanTypeTestSession)
	if len(sessionSpans) != 1 {
		panic(fmt.Sprintf("expected exactly one session span, got %d", len(sessionSpans)))
	}
}

func checkIntelligentTestRunnerSkipTags(finishedSpans []*mocktracer.Span) {
	checkSpansByTagValue(finishedSpans, constants.TestStatus, constants.TestStatusSkip, 6)
	checkSpansByTagValue(finishedSpans, constants.TestSkippedByITR, "true", 5)
	checkSpansByTagValue(finishedSpans, constants.TestSkipReason, constants.SkippedByITRReason, 5)
	checkSpansByTagValue(finishedSpans, constants.TestFinalStatus, constants.TestStatusSkip, 6)
	checkSpansByTagValue(finishedSpans, constants.TestUnskippable, "true", 7)
	checkSpansByTagValue(finishedSpans, constants.TestForcedToRun, "true", 1)
	checkITRTestsSkippingEnabledTag(finishedSpans, "true")
}

func runTestManagementTests(m *testing.M) {
	// mock the settings api to enable quarantine and disable tests
	server := setUpHTTPServer(false, false, false, nil, false, nil, true,
		&net.TestManagementTestsResponseDataModules{
			Modules: map[string]net.TestManagementTestsResponseDataSuites{
				"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting": {
					Suites: map[string]net.TestManagementTestsResponseDataTests{
						"reflections_test.go": {
							Tests: map[string]net.TestManagementTestsResponseDataTestProperties{
								"TestGetFieldPointerFrom": {
									Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{
										Quarantined:  true,
										AttemptToFix: true,
									},
								},
								"TestGetInternalTestArray": {
									Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{
										Disabled:     true,
										AttemptToFix: true,
									},
								},
							},
						},
						"testing_test.go": {
							Tests: map[string]net.TestManagementTestsResponseDataTestProperties{
								"TestMyTest01": {
									Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{
										Disabled: true,
									},
								},
								"TestRetryWithFail": {
									Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{
										Quarantined: true,
									},
								},
								"TestRetryWithPanic": {
									Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{
										Disabled:     true,
										AttemptToFix: true,
									},
								},
							},
						},
					},
				},
			},
		},
		false,
		nil)

	defer server.Close()

	// set a custom retry count
	os.Setenv(constants.CIVisibilityTestManagementAttemptToFixRetriesEnvironmentVariable, "10")

	// initialize the mock tracer for doing assertions on the finished spans
	currentM = m
	mTracer = integrations.InitializeCIVisibilityMock()

	testRetryWithPanicRunNumber.Store(-10) // this makes TestRetryWithPanic to always fail (required by this test)
	exitCode := RunM(m)
	if exitCode != 0 {
		panic("expected the exit code to be 0. Got exit code: " + strconv.Itoa(exitCode))
	}

	// get all finished spans
	finishedSpans := mTracer.FinishedSpans()
	showResourcesNameFromSpans(finishedSpans)

	// Disabled test with an attempt to fix with 10 executions
	testGetInternalTestArray := checkSpansByResourceName(finishedSpans, "reflections_test.go.TestGetInternalTestArray", 10)
	checkSpansByTagValue(testGetInternalTestArray, constants.TestIsDisabled, "true", 10)               // Disabled
	checkSpansByTagValue(testGetInternalTestArray, constants.TestIsAttempToFix, "true", 10)            // Is an attempt to fix
	checkSpansByTagValue(testGetInternalTestArray, constants.TestIsRetry, "true", 9)                   // 9 retries
	checkSpansByTagValue(testGetInternalTestArray, constants.TestRetryReason, "attempt_to_fix", 9)     // 9 retries with the attempt to fix reason
	checkSpansByTagValue(testGetInternalTestArray, constants.TestAttemptToFixPassed, "true", 1)        // Attempt to fix passed (reported in the latest retry)
	checkSpansByTagValue(testGetInternalTestArray, constants.TestAttemptToFixPassed, "false", 0)       // Attempt to fix passed false (reported in the latest retry)
	checkSpansByTagValue(testGetInternalTestArray, constants.TestHasFailedAllRetries, "true", 0)       // All retries failed = false (reported in the latest retry)
	checkSpansByTagValue(testGetInternalTestArray, constants.TestStatus, constants.TestStatusPass, 10) // All tests passed

	// Quaratined test with an attempt to fix with 10 executions
	testGetFieldPointerFrom := checkSpansByResourceName(finishedSpans, "reflections_test.go.TestGetFieldPointerFrom", 10)
	checkSpansByTagValue(testGetFieldPointerFrom, constants.TestIsQuarantined, "true", 10)            // Quarantined
	checkSpansByTagValue(testGetFieldPointerFrom, constants.TestIsAttempToFix, "true", 10)            // Is an attempt to fix
	checkSpansByTagValue(testGetFieldPointerFrom, constants.TestIsRetry, "true", 9)                   // 9 retries
	checkSpansByTagValue(testGetFieldPointerFrom, constants.TestRetryReason, "attempt_to_fix", 9)     // 9 retries with the attempt to fix reason
	checkSpansByTagValue(testGetFieldPointerFrom, constants.TestAttemptToFixPassed, "true", 1)        // Attempt to fix passed (reported in the latest retry)
	checkSpansByTagValue(testGetFieldPointerFrom, constants.TestAttemptToFixPassed, "false", 0)       // Attempt to fix passed false (reported in the latest retry)
	checkSpansByTagValue(testGetFieldPointerFrom, constants.TestHasFailedAllRetries, "true", 0)       // All retries failed = false (reported in the latest retry)
	checkSpansByTagValue(testGetFieldPointerFrom, constants.TestStatus, constants.TestStatusPass, 10) // All tests passed

	// Disabled test without an attempt to fix (it just skipped and reported as skipped)
	testMyTest01 := checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest01", 1)
	checkSpansByTagValue(testMyTest01, constants.TestIsDisabled, "true", 1)               // Disabled
	checkSpansByTagValue(testMyTest01, constants.TestIsAttempToFix, "true", 0)            // Is not an attempt to fix
	checkSpansByTagValue(testMyTest01, constants.TestIsRetry, "true", 0)                  // 0 retries
	checkSpansByTagValue(testMyTest01, constants.TestRetryReason, "attempt_to_fix", 0)    // 0 retries with the attempt to fix reason
	checkSpansByTagValue(testMyTest01, constants.TestHasFailedAllRetries, "true", 0)      // All retries failed (reported in the latest retry)
	checkSpansByTagValue(testMyTest01, constants.TestAttemptToFixPassed, "true", 0)       // Attempt to fix passed false (reported in the latest retry)
	checkSpansByTagValue(testMyTest01, constants.TestAttemptToFixPassed, "false", 0)      // Attempt to fix passed false (reported in the latest retry)
	checkSpansByTagValue(testMyTest01, constants.TestStatus, constants.TestStatusSkip, 1) // Because is not an attempt to fix we just skip it
	checkSpansByTagValue(testMyTest01, constants.TestSkipReason, constants.TestDisabledSkipReason, 1)

	// Quarantined test without an attempt to fix (it executed but reported as skipped)
	testRetryWithFail := checkSpansByResourceName(finishedSpans, "testing_test.go.TestRetryWithFail", 1)
	checkSpansByTagValue(testRetryWithFail, constants.TestIsQuarantined, "true", 1)            // Quarantined
	checkSpansByTagValue(testRetryWithFail, constants.TestIsAttempToFix, "true", 0)            // Is not an attempt to fix
	checkSpansByTagValue(testRetryWithFail, constants.TestIsRetry, "true", 0)                  // 0 retries
	checkSpansByTagValue(testRetryWithFail, constants.TestRetryReason, "attempt_to_fix", 0)    // 0 retries with the attempt to fix reason
	checkSpansByTagValue(testRetryWithFail, constants.TestHasFailedAllRetries, "true", 0)      // All retries failed (reported in the latest retry)
	checkSpansByTagValue(testRetryWithFail, constants.TestAttemptToFixPassed, "true", 0)       // Attempt to fix passed false (reported in the latest retry)
	checkSpansByTagValue(testRetryWithFail, constants.TestAttemptToFixPassed, "false", 0)      // Attempt to fix passed false (reported in the latest retry)
	checkSpansByTagValue(testRetryWithFail, constants.TestStatus, constants.TestStatusFail, 1) // Because is not an attempt to fix we execute it but don't report the status

	// Disabled test with an attempt to fix with 10 executions
	testRetryWithPanic := checkSpansByResourceName(finishedSpans, "testing_test.go.TestRetryWithPanic", 10)
	checkSpansByTagValue(testRetryWithPanic, constants.TestIsDisabled, "true", 10)               // Disabled
	checkSpansByTagValue(testRetryWithPanic, constants.TestIsAttempToFix, "true", 10)            // Is an attempt to fix
	checkSpansByTagValue(testRetryWithPanic, constants.TestIsRetry, "true", 9)                   // 9 retries
	checkSpansByTagValue(testRetryWithPanic, constants.TestRetryReason, "attempt_to_fix", 9)     // 9 retries with the attempt to fix reason
	checkSpansByTagValue(testRetryWithPanic, constants.TestHasFailedAllRetries, "true", 1)       // All retries failed (reported in the latest retry)
	checkSpansByTagValue(testRetryWithPanic, constants.TestAttemptToFixPassed, "true", 0)        // Attempt to fix passed false (reported in the latest retry)
	checkSpansByTagValue(testRetryWithPanic, constants.TestAttemptToFixPassed, "false", 1)       // Attempt to fix passed false (reported in the latest retry)
	checkSpansByTagValue(testRetryWithPanic, constants.TestStatus, constants.TestStatusFail, 10) // All tests passed

	// check test.final_status - only on final execution span for tests with retries
	// Disabled with ATF (testGetInternalTestArray): isDisabled=true -> final_status=skip on last execution
	checkSpansByTagValue(testGetInternalTestArray, constants.TestFinalStatus, constants.TestStatusSkip, 1)

	// Quarantined with ATF (testGetFieldPointerFrom): isQuarantined=true -> final_status=skip on last execution
	checkSpansByTagValue(testGetFieldPointerFrom, constants.TestFinalStatus, constants.TestStatusSkip, 1)

	// Disabled without ATF (testMyTest01): isDisabled=true -> final_status=skip (set before close)
	checkSpansByTagValue(testMyTest01, constants.TestFinalStatus, constants.TestStatusSkip, 1)

	// Quarantined without ATF (testRetryWithFail): isQuarantined=true -> final_status=skip on final execution
	checkSpansByTagValue(testRetryWithFail, constants.TestFinalStatus, constants.TestStatusSkip, 1)

	// Disabled with ATF all fail (testRetryWithPanic): isDisabled=true -> final_status=skip on last execution
	checkSpansByTagValue(testRetryWithPanic, constants.TestFinalStatus, constants.TestStatusSkip, 1)

	// check capabilities tags
	checkCapabilitiesTags(finishedSpans)

	// check if suite has both test code owners and source file tags
	suiteSpans := getSpansWithType(finishedSpans, constants.SpanTypeTestSuite)
	checkSpansByTagName(suiteSpans, constants.TestCodeOwners, 4)
	checkSpansByTagName(suiteSpans, constants.TestSourceFile, 4)

	// check logs
	checkLogs()

	os.Exit(0)
}

func checkSpansByType(finishedSpans []*mocktracer.Span,
	totalFinishedSpansCount int, sessionSpansCount int, moduleSpansCount int,
	suiteSpansCount int, testSpansCount int, normalSpansCount int) {
	calculatedFinishedSpans := len(finishedSpans)
	log.Debug("Number of spans received: %d", calculatedFinishedSpans)
	if calculatedFinishedSpans < totalFinishedSpansCount {
		panic(fmt.Sprintf("expected at least %d finished spans, got %d", totalFinishedSpansCount, calculatedFinishedSpans))
	}

	sessionSpans := getSpansWithType(finishedSpans, constants.SpanTypeTestSession)
	calculatedSessionSpans := len(sessionSpans)
	log.Debug("Number of sessions received: %d", calculatedSessionSpans)
	if calculatedSessionSpans != sessionSpansCount {
		panic(fmt.Sprintf("expected exactly %d session span, got %d", sessionSpansCount, calculatedSessionSpans))
	}

	moduleSpans := getSpansWithType(finishedSpans, constants.SpanTypeTestModule)
	calculatedModuleSpans := len(moduleSpans)
	log.Debug("Number of modules received: %d", calculatedModuleSpans)
	if calculatedModuleSpans != moduleSpansCount {
		panic(fmt.Sprintf("expected exactly %d module span, got %d", moduleSpansCount, calculatedModuleSpans))
	}

	suiteSpans := getSpansWithType(finishedSpans, constants.SpanTypeTestSuite)
	calculatedSuiteSpans := len(suiteSpans)
	log.Debug("Number of suites received: %d", calculatedSuiteSpans)
	if calculatedSuiteSpans != suiteSpansCount {
		panic(fmt.Sprintf("expected exactly %d suite spans, got %d", suiteSpansCount, calculatedSuiteSpans))
	}

	testSpans := getSpansWithType(finishedSpans, constants.SpanTypeTest)
	calculatedTestSpans := len(testSpans)
	log.Debug("Number of tests received: %d", calculatedTestSpans)
	if calculatedTestSpans != testSpansCount {
		panic(fmt.Sprintf("expected exactly %d test spans, got %d", testSpansCount, calculatedTestSpans))
	}

	normalSpans := getSpansWithType(finishedSpans, ext.SpanTypeHTTP)
	calculatedNormalSpans := len(normalSpans)
	log.Debug("Number of http spans received: %d", calculatedNormalSpans)
	if calculatedNormalSpans != normalSpansCount {
		panic(fmt.Sprintf("expected exactly %d normal spans, got %d", normalSpansCount, calculatedNormalSpans))
	}
}

func checkSpansByResourceName(finishedSpans []*mocktracer.Span, resourceName string, count int) []*mocktracer.Span {
	spans := getSpansWithResourceName(finishedSpans, resourceName)
	numOfSpans := len(spans)
	if numOfSpans != count {
		panic(fmt.Sprintf("expected exactly %d spans with resource name: %s, got %d", count, resourceName, numOfSpans))
	}

	return spans
}

func checkSpansByTagName(finishedSpans []*mocktracer.Span, tagName string, count int) []*mocktracer.Span {
	spans := getSpansWithTagName(finishedSpans, tagName)
	numOfSpans := len(spans)
	if numOfSpans != count {
		panic(fmt.Sprintf("expected exactly %d spans with tag name: %s, got %d", count, tagName, numOfSpans))
	}

	return spans
}

func checkSpansByTagValue(finishedSpans []*mocktracer.Span, tagName, tagValue string, count int) []*mocktracer.Span {
	spans := getSpansWithTagNameAndValue(finishedSpans, tagName, tagValue)
	numOfSpans := len(spans)
	if numOfSpans != count {
		panic(fmt.Sprintf("expected exactly %d spans with tag name: %s and value %s, got %d", count, tagName, tagValue, numOfSpans))
	}

	return spans
}

func checkCapabilitiesTags(finishedSpans []*mocktracer.Span) {
	tests := getSpansWithType(finishedSpans, constants.SpanTypeTest)
	numOfTests := len(tests)
	if len(getSpansWithTagName(tests, constants.LibraryCapabilitiesTestImpactAnalysis)) != numOfTests {
		panic(fmt.Sprintf("expected all test spans to have the %s tag", constants.LibraryCapabilitiesTestImpactAnalysis))
	}
	if len(getSpansWithTagName(tests, constants.LibraryCapabilitiesEarlyFlakeDetection)) != numOfTests {
		panic(fmt.Sprintf("expected all test spans to have the %s tag", constants.LibraryCapabilitiesEarlyFlakeDetection))
	}
	if len(getSpansWithTagName(tests, constants.LibraryCapabilitiesAutoTestRetries)) != numOfTests {
		panic(fmt.Sprintf("expected all test spans to have the %s tag", constants.LibraryCapabilitiesAutoTestRetries))
	}
	if len(getSpansWithTagName(tests, constants.LibraryCapabilitiesCoverageReportUpload)) != numOfTests {
		panic(fmt.Sprintf("expected all test spans to have the %s tag", constants.LibraryCapabilitiesCoverageReportUpload))
	}
	if len(getSpansWithTagName(tests, constants.LibraryCapabilitiesTestManagementQuarantine)) != numOfTests {
		panic(fmt.Sprintf("expected all test spans to have the %s tag", constants.LibraryCapabilitiesTestManagementQuarantine))
	}
	if len(getSpansWithTagName(tests, constants.LibraryCapabilitiesTestManagementDisable)) != numOfTests {
		panic(fmt.Sprintf("expected all test spans to have the %s tag", constants.LibraryCapabilitiesTestManagementDisable))
	}
	if len(getSpansWithTagName(tests, constants.LibraryCapabilitiesTestManagementAttemptToFix)) != numOfTests {
		panic(fmt.Sprintf("expected all test spans to have the %s tag", constants.LibraryCapabilitiesTestManagementAttemptToFix))
	}
}

func checkITRTestsSkippingEnabledTag(finishedSpans []*mocktracer.Span, tagValue string) {
	for _, spanType := range []struct {
		name string
		typ  string
	}{
		{name: "session", typ: constants.SpanTypeTestSession},
		{name: "module", typ: constants.SpanTypeTestModule},
		{name: "suite", typ: constants.SpanTypeTestSuite},
		{name: "test", typ: constants.SpanTypeTest},
	} {
		for _, sp := range getSpansWithType(finishedSpans, spanType.typ) {
			if got := sp.Tag(constants.ITRTestsSkippingEnabled); got != tagValue {
				panic(fmt.Sprintf("expected %s %s=%s on %s, got %v",
					spanType.name,
					constants.ITRTestsSkippingEnabled,
					tagValue,
					sp.Tag(ext.ResourceName),
					got))
			}
		}
	}
}

func checkLogs() {
	// Assert that at least one logs payload has been sent by the library.
	logsEntriesCount := len(logsEntries)
	log.Debug("Number of logs received: %d", logsEntriesCount)
	if logsEntriesCount == 0 {
		panic("expected at least one logs payload to be sent, but none were received")
	}
}

type (
	skippableResponse struct {
		Meta skippableResponseMeta   `json:"meta"`
		Data []skippableResponseData `json:"data"`
	}

	skippableResponseMeta struct {
		CorrelationID string            `json:"correlation_id"`
		Coverage      map[string]string `json:"coverage,omitempty"`
	}

	skippableResponseData struct {
		ID         string                              `json:"id"`
		Type       string                              `json:"type"`
		Attributes net.SkippableResponseDataAttributes `json:"attributes"`
	}
)

func setUpHTTPServer(
	flakyRetriesEnabled bool,
	knownTestsEnabled bool,
	earlyFlakyDetectionEnabled bool,
	earlyFlakyDetectionData *net.KnownTestsResponseData,
	itrEnabled bool,
	itrData []net.SkippableResponseDataAttributes,
	testManagement bool,
	testManagementData *net.TestManagementTestsResponseDataModules,
	impactedTests bool,
	itrCoverage map[string][]byte) *httptest.Server {
	isolateReadCacheForMockServer()

	// Reset the collected logs for the new server instance.
	logsEntries = nil
	enableKnownTests := knownTestsEnabled || earlyFlakyDetectionEnabled
	// mock the settings api to enable automatic test retries
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Debug("MockApi received request: %s", r.URL.Path)

		// Settings request
		if r.URL.Path == "/api/v2/libraries/tests/services/setting" {
			body, _ := io.ReadAll(r.Body)
			log.Debug("MockApi received body: %s", body)
			w.Header().Set("Content-Type", "application/json")
			response := struct {
				Data struct {
					ID         string                   `json:"id"`
					Type       string                   `json:"type"`
					Attributes net.SettingsResponseData `json:"attributes"`
				} `json:"data"`
			}{}

			// let's enable flaky test retries
			response.Data.Attributes = net.SettingsResponseData{
				FlakyTestRetriesEnabled: flakyRetriesEnabled,
				ItrEnabled:              itrEnabled,
				TestsSkipping:           itrEnabled,
				KnownTestsEnabled:       enableKnownTests,
				ImpactedTestsEnabled:    impactedTests,
			}

			response.Data.Attributes.TestManagement.Enabled = testManagement

			response.Data.Attributes.EarlyFlakeDetection.Enabled = earlyFlakyDetectionEnabled
			response.Data.Attributes.EarlyFlakeDetection.SlowTestRetries.FiveS = 10
			response.Data.Attributes.EarlyFlakeDetection.SlowTestRetries.TenS = 5
			response.Data.Attributes.EarlyFlakeDetection.SlowTestRetries.ThirtyS = 3
			response.Data.Attributes.EarlyFlakeDetection.SlowTestRetries.FiveM = 2

			log.Debug("MockApi sending response: %v", response)
			json.NewEncoder(w).Encode(&response)
		} else if enableKnownTests && r.URL.Path == "/api/v2/ci/libraries/tests" {
			body, _ := io.ReadAll(r.Body)
			log.Debug("MockApi received body: %s", body)
			w.Header().Set("Content-Type", "application/json")
			response := struct {
				Data struct {
					ID         string                     `json:"id"`
					Type       string                     `json:"type"`
					Attributes net.KnownTestsResponseData `json:"attributes"`
				} `json:"data"`
			}{}

			if earlyFlakyDetectionData != nil {
				response.Data.Attributes = *earlyFlakyDetectionData
			}

			log.Debug("MockApi sending response: %v", response)
			json.NewEncoder(w).Encode(&response)
		} else if r.URL.Path == "/api/v2/git/repository/search_commits" {
			body, _ := io.ReadAll(r.Body)
			log.Debug("MockApi received body: %s", body)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("{}"))
		} else if r.URL.Path == "/api/v2/git/repository/packfile" {
			w.WriteHeader(http.StatusAccepted)
		} else if itrEnabled && r.URL.Path == "/api/v2/ci/tests/skippable" {
			body, _ := io.ReadAll(r.Body)
			log.Debug("MockApi received body: %s", body)
			w.Header().Set("Content-Type", "application/json")
			response := skippableResponse{
				Meta: skippableResponseMeta{
					CorrelationID: "correlation_id",
				},
				Data: []skippableResponseData{},
			}
			if itrCoverage != nil {
				response.Meta.Coverage = make(map[string]string, len(itrCoverage))
				for file, bitmap := range itrCoverage {
					response.Meta.Coverage[file] = base64.StdEncoding.EncodeToString(bitmap)
				}
			}
			for i, data := range itrData {
				response.Data = append(response.Data, skippableResponseData{
					ID:         fmt.Sprintf("id_%d", i),
					Type:       "type",
					Attributes: data,
				})
			}
			log.Debug("MockApi sending response: %v", response)
			json.NewEncoder(w).Encode(&response)
		} else if r.URL.Path == "/api/v2/logs" {
			// Mock the logs intake endpoint.
			reader, _ := gzip.NewReader(r.Body)
			body, _ := io.ReadAll(reader)
			log.Debug("MockApi received logs payload: %d bytes", len(body))
			var newEntries []*mockedLogEntry
			if err := json.Unmarshal(body, &newEntries); err != nil {
				log.Debug("MockApi received invalid logs payload: %s", err)
				log.Debug("Payload: %s", body)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			logsEntries = append(logsEntries, newEntries...)
			log.Debug("MockApi received %d log entries", len(newEntries))
			// A 2xx status code is required to mark the payload as accepted.
			w.WriteHeader(http.StatusAccepted)
		} else if r.URL.Path == "/api/v2/test/libraries/test-management/tests" {
			body, _ := io.ReadAll(r.Body)
			log.Debug("MockApi received body: %s", body)
			w.Header().Set("Content-Type", "application/json")
			response := struct {
				Data struct {
					ID         string                                     `json:"id"`
					Type       string                                     `json:"type"`
					Attributes net.TestManagementTestsResponseDataModules `json:"attributes"`
				} `json:"data"`
			}{}
			response.Data.Type = "ci_app_libraries_tests"
			response.Data.Attributes = *testManagementData
			log.Debug("MockApi sending response: %v", response)
			json.NewEncoder(w).Encode(&response)
		} else {
			http.NotFound(w, r)
		}
	}))

	// set the custom agentless url and the flaky retry count env-var
	log.Debug("Using mockapi at: %s", server.URL)
	os.Setenv(constants.CIVisibilityAgentlessEnabledEnvironmentVariable, "1")
	os.Setenv(constants.CIVisibilityAgentlessURLEnvironmentVariable, server.URL)
	os.Setenv(constants.APIKeyEnvironmentVariable, "12345")

	return server
}

func isolateReadCacheForMockServer() {
	// httptest ports can be reused between scenario subprocesses, so keep mock
	// CI Visibility responses out of the shared short-lived read cache.
	cacheRoot, err := os.MkdirTemp("", "dd-trace-go-civisibility-read-cache-*")
	if err != nil {
		log.Debug("unable to isolate CI Visibility read cache for mock server: %s", err.Error())
		return
	}
	net.SetReadCacheHooksForTesting(cacheRoot, nil, nil, nil, nil)
	integrations.PushCiVisibilityCloseAction(func() {
		net.ResetReadCacheHooksForTesting()
		_ = os.RemoveAll(cacheRoot)
	})
}

func getSpansWithType(spans []*mocktracer.Span, spanType string) []*mocktracer.Span {
	var result []*mocktracer.Span
	for _, span := range spans {
		if span.Tag(ext.SpanType) == spanType {
			result = append(result, span)
		}
	}

	return result
}

func getSpansWithResourceName(spans []*mocktracer.Span, resourceName string) []*mocktracer.Span {
	var result []*mocktracer.Span
	for _, span := range spans {
		if span.Tag(ext.ResourceName) == resourceName {
			result = append(result, span)
		}
	}

	return result
}

func getSpansWithTagName(spans []*mocktracer.Span, tag string) []*mocktracer.Span {
	var result []*mocktracer.Span
	for _, span := range spans {
		if span.Tag(tag) != nil {
			result = append(result, span)
		}
	}

	return result
}

func getSpansWithTagNameAndValue(spans []*mocktracer.Span, tag, value string) []*mocktracer.Span {
	var result []*mocktracer.Span
	for _, span := range spans {
		if span.Tag(tag) == value {
			result = append(result, span)
		}
	}

	return result
}

func showResourcesNameFromSpans(spans []*mocktracer.Span) {
	for i, span := range spans {
		log.Debug("  [%d] = %v | %v", i, span.Tag(ext.ResourceName), span.Tag(constants.TestName))
	}
}

type mockedLogEntry struct {
	DdSource   string `json:"ddsource"`
	Hostname   string `json:"hostname"`
	Timestamp  int64  `json:"timestamp,omitempty"`
	Message    string `json:"message"`
	DdTraceID  string `json:"dd.trace_id"`
	DdSpanID   string `json:"dd.span_id"`
	TestModule string `json:"test.module"`
	TestSuite  string `json:"test.suite"`
	TestName   string `json:"test.name"`
	Service    string `json:"service"`
	DdTags     string `json:"dd_tags,omitempty"`
}

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gotesting

import (
	"bufio"
	"fmt"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting/coverage"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/logs"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/telemetry"
)

const (
	// testFramework represents the name of the testing framework.
	testFramework = "golang.org/pkg/testing"
)

var (
	// session represents the CI visibility test session.
	session integrations.TestSession

	// testInfos holds information about the instrumented tests.
	testInfos []*testingTInfo

	// benchmarkInfos holds information about the instrumented benchmarks.
	benchmarkInfos []*testingBInfo

	// modulesCountersMutex is a mutex to protect access to the modulesCounters map.
	modulesCountersMutex sync.Mutex

	// modulesCounters keeps track of the number of tests per module.
	modulesCounters = map[string]int{}

	// suitesCountersMutex is a mutex to protect access to the suitesCounters map.
	suitesCountersMutex sync.Mutex

	// suitesCounters keeps track of the number of tests per suite.
	suitesCounters = map[string]int{}

	// numOfTestsSkipped keeps track of the number of tests skipped by ITR.
	numOfTestsSkipped atomic.Uint64

	// chattyPrinterOnce ensures that the chatty printer is initialized only once.
	chattyPrinterOnce sync.Once

	// chatty is the global chatty printer used for debugging and verbose output.
	chatty *chattyPrinter
)

type (
	// commonInfo holds common information about tests and benchmarks.
	commonInfo struct {
		moduleName string
		suiteName  string
		testName   string
	}

	// testingTInfo holds information specific to tests.
	testingTInfo struct {
		commonInfo
		originalFunc func(*testing.T)
	}

	// testingBInfo holds information specific to benchmarks.
	testingBInfo struct {
		commonInfo
		originalFunc func(b *testing.B)
	}

	// M is a wrapper around testing.M to provide instrumentation.
	M testing.M
)

// Run initializes CI Visibility, instruments tests and benchmarks, and runs them.
func (ddm *M) Run() int {
	m := (*testing.M)(ddm)

	// Instrument testing.M
	exitFn := instrumentTestingM(m)

	// Run the tests and benchmarks.
	var exitCode = m.Run()

	// Finalize instrumentation
	exitFn(exitCode)
	return exitCode
}

// instrumentInternalTests instruments the internal tests for CI visibility.
func (ddm *M) instrumentInternalTests(internalTests *[]testing.InternalTest) {
	if internalTests == nil {
		return
	}

	// Get the settings response for this session
	settings := integrations.GetSettings()

	// Check if the test is going to be skipped by ITR
	if settings.ItrEnabled {
		if settings.CodeCoverage && coverage.CanCollect() {
			session.SetTag(constants.CodeCoverageEnabled, "true")
		} else {
			session.SetTag(constants.CodeCoverageEnabled, "true")
		}

		if settings.TestsSkipping {
			session.SetTag(constants.ITRTestsSkippingEnabled, "true")
			session.SetTag(constants.ITRTestsSkippingType, "test")

			// Check if the test is going to be skipped by ITR
			skippableTests := integrations.GetSkippableTests()
			if skippableTests != nil {
				if len(skippableTests) > 0 {
					session.SetTag(constants.ITRTestsSkipped, "false")
				}
			}
		} else {
			session.SetTag(constants.ITRTestsSkippingEnabled, "false")
		}
	}

	// Extract info from internal tests
	testInfos = make([]*testingTInfo, len(*internalTests))
	for idx, test := range *internalTests {
		moduleName, suiteName := utils.GetModuleAndSuiteName(reflect.Indirect(reflect.ValueOf(test.F)).Pointer())
		testInfo := &testingTInfo{
			originalFunc: test.F,
			commonInfo: commonInfo{
				moduleName: moduleName,
				suiteName:  suiteName,
				testName:   test.Name,
			},
		}

		// Increment the test count in the module.
		addModulesCounters(moduleName, 1)

		// Increment the test count in the suite.
		addSuitesCounters(suiteName, 1)

		testInfos[idx] = testInfo
	}

	// Create new instrumented internal tests
	newTestArray := make([]testing.InternalTest, len(*internalTests))
	for idx, testInfo := range testInfos {
		newTestArray[idx] = testing.InternalTest{
			Name: testInfo.testName,
			F:    ddm.executeInternalTest(testInfo),
		}
	}
	*internalTests = newTestArray
}

// executeInternalTest wraps the original test function to include CI visibility instrumentation.
func (ddm *M) executeInternalTest(testInfo *testingTInfo) func(*testing.T) {
	originalFunc := runtime.FuncForPC(reflect.Indirect(reflect.ValueOf(testInfo.originalFunc)).Pointer())

	// Get the settings response for this session
	settings := integrations.GetSettings()
	coverageEnabled := settings.CodeCoverage
	testSkippedByITR := false
	testIsNew := true

	// Check if the test is going to be skipped by ITR
	if settings.ItrEnabled && settings.TestsSkipping {
		// Check if the test is going to be skipped by ITR
		skippableTests := integrations.GetSkippableTests()
		if skippableTests != nil {
			if suitesMap, ok := skippableTests[testInfo.suiteName]; ok {
				if _, ok := suitesMap[testInfo.testName]; ok {
					testSkippedByITR = true
				}
			}
		}
	}

	// Check if the test is known
	if settings.KnownTestsEnabled {
		testIsKnown, testKnownDataOk := isKnownTest(&testInfo.commonInfo)
		testIsNew = testKnownDataOk && !testIsKnown
	} else {
		// We don't mark any test as new if the feature is disabled
		testIsNew = false
	}

	// Instrument the test function
	instrumentedFunc := func(t *testing.T) {
		// Set this func as a helper func of t
		t.Helper()

		// Get the metadata regarding the execution (in case is already created from the additional features)
		execMeta := getTestMetadata(t)
		if execMeta == nil {
			// in case there's no additional features then we create the metadata for this execution and defer the disposal
			execMeta = createTestMetadata(t, nil)
			defer deleteTestMetadata(t)
		}

		// Create or retrieve the module, suite, and test for CI visibility.
		module := session.GetOrCreateModule(testInfo.moduleName)
		suite := module.GetOrCreateSuite(testInfo.suiteName)
		test := suite.CreateTest(testInfo.testName)
		test.SetTestFunc(originalFunc)

		// If the execution is for a new test we tag the test event as new
		execMeta.isANewTest = execMeta.isANewTest || testIsNew

		// Set some required tags from the execution metadata
		cancelExecution := setTestTagsFromExecutionMetadata(test, execMeta)
		if cancelExecution {
			return
		}

		// Check if the test needs to be skipped by ITR (attempt to fix is excluded)
		if testSkippedByITR && !execMeta.isAttemptToFix && !execMeta.isAModifiedTest {
			// check if the test was marked as unskippable
			if test.Context().Value(constants.TestUnskippable) != true {
				test.SetTag(constants.TestSkippedByITR, "true")
				test.Close(integrations.ResultStatusSkip, integrations.WithTestSkipReason(constants.SkippedByITRReason))
				telemetry.ITRSkipped(telemetry.TestEventType)
				session.SetTag(constants.ITRTestsSkipped, "true")
				session.SetTag(constants.ITRTestsSkippingCount, numOfTestsSkipped.Add(1))
				checkModuleAndSuite(module, suite)
				t.Skip(constants.SkippedByITRReason)
				return
			}
			test.SetTag(constants.TestForcedToRun, "true")
			telemetry.ITRForcedRun(telemetry.TestEventType)
		}

		// Check if the coverage is enabled
		var tCoverage coverage.TestCoverage
		var tParentOldBarrier chan bool
		if coverageEnabled && coverage.CanCollect() {
			// set the test coverage collector
			testFile, _ := originalFunc.FileLine(originalFunc.Entry())
			tCoverage = coverage.NewTestCoverage(
				session.SessionID(),
				module.ModuleID(),
				suite.SuiteID(),
				test.TestID(),
				testFile)

			// now we need to disable parallelism for the test in order to collect the test coverage
			tParent := getTestParentPrivateFields(t)
			if tParent != nil && tParent.barrier != nil {
				tParentOldBarrier = *tParent.barrier
				*tParent.barrier = nil
			}
		}

		// Initialize the chatty printer if not already done.
		instrumentChattyPrinter(t)

		startTime := time.Now()
		defer func() {
			duration := time.Since(startTime)

			if tCoverage != nil {
				// Collect coverage after test execution so we can calculate the diff comparing to the baseline.
				tCoverage.CollectCoverageAfterTestExecution()

				// now we restore the original parent barrier
				tParent := getTestParentPrivateFields(t)
				if tParent != nil && tParent.barrier != nil {
					*tParent.barrier = tParentOldBarrier
				}
			}

			// check if is a new EFD test and the duration >= 5 min
			if execMeta.isANewTest && duration.Minutes() >= 5 {
				// Set the EFD retry abort reason
				test.SetTag(constants.TestEarlyFlakeDetectionRetryAborted, "slow")
			}

			// Collect and write logs
			collectAndWriteLogs(t, test)

			if r := recover(); r != nil {
				// Handle panic and set error information.
				execMeta.panicData = r
				execMeta.panicStacktrace = utils.GetStacktrace(1)
				if execMeta.isARetry && execMeta.isLastRetry {
					if execMeta.allRetriesFailed {
						test.SetTag(constants.TestHasFailedAllRetries, "true")
					}
					if execMeta.isAttemptToFix {
						test.SetTag(constants.TestAttemptToFixPassed, "false")
					}
				}
				test.SetError(integrations.WithErrorInfo("panic", fmt.Sprint(r), execMeta.panicStacktrace))
				suite.SetTag(ext.Error, true)
				module.SetTag(ext.Error, true)
				test.Close(integrations.ResultStatusFail)
				if !execMeta.hasAdditionalFeatureWrapper {
					// we are going to let the additional feature wrapper to handle
					// the panic, and module and suite closing (we don't want to close the suite earlier in case of a retry)
					checkModuleAndSuite(module, suite)
					integrations.ExitCiVisibility()
					panic(r)
				}
			} else {
				// Normal finalization: determine the test result based on its state.
				if t.Failed() {
					if execMeta.isARetry && execMeta.isLastRetry {
						if execMeta.allRetriesFailed {
							test.SetTag(constants.TestHasFailedAllRetries, "true")
						}
						if execMeta.isAttemptToFix {
							test.SetTag(constants.TestAttemptToFixPassed, "false")
						}
					}
					test.SetTag(ext.Error, true)
					suite.SetTag(ext.Error, true)
					module.SetTag(ext.Error, true)
					test.Close(integrations.ResultStatusFail)
				} else if t.Skipped() {
					if execMeta.isAttemptToFix && execMeta.isARetry && execMeta.isLastRetry {
						test.SetTag(constants.TestAttemptToFixPassed, "false")
					}
					test.Close(integrations.ResultStatusSkip)
				} else {
					if execMeta.isAttemptToFix && execMeta.isARetry && execMeta.isLastRetry {
						if execMeta.allAttemptsPassed {
							test.SetTag(constants.TestAttemptToFixPassed, "true")
						} else {
							test.SetTag(constants.TestAttemptToFixPassed, "false")
						}
					}
					test.Close(integrations.ResultStatusPass)
				}

				if !execMeta.hasAdditionalFeatureWrapper {
					// we are going to let the additional feature wrapper to handle
					// the module and suite closing (we don't want to close the suite earlier in case of a retry)
					checkModuleAndSuite(module, suite)
				}
			}
		}()

		if tCoverage != nil {
			// Collect coverage before test execution so we can register a baseline.
			tCoverage.CollectCoverageBeforeTestExecution()
		}

		// Execute the original test function.
		testInfo.originalFunc(t)
	}

	// Register the instrumented func as an internal instrumented func (to avoid double instrumentation)
	setInstrumentationMetadata(runtime.FuncForPC(reflect.Indirect(reflect.ValueOf(instrumentedFunc)).Pointer()), &instrumentationMetadata{IsInternal: true})

	// If the test is going to be skipped by ITR then we don't apply the additional features
	if testSkippedByITR {
		return instrumentedFunc
	}

	// Get the additional feature wrapper
	return applyAdditionalFeaturesToTestFunc(instrumentedFunc, &testInfo.commonInfo)
}

// instrumentInternalBenchmarks instruments the internal benchmarks for CI visibility.
func (ddm *M) instrumentInternalBenchmarks(internalBenchmarks *[]testing.InternalBenchmark) {
	if internalBenchmarks == nil {
		return
	}

	// Extract info from internal benchmarks
	benchmarkInfos = make([]*testingBInfo, len(*internalBenchmarks))
	for idx, benchmark := range *internalBenchmarks {
		moduleName, suiteName := utils.GetModuleAndSuiteName(reflect.Indirect(reflect.ValueOf(benchmark.F)).Pointer())
		benchmarkInfo := &testingBInfo{
			originalFunc: benchmark.F,
			commonInfo: commonInfo{
				moduleName: moduleName,
				suiteName:  suiteName,
				testName:   benchmark.Name,
			},
		}

		// Increment the test count in the module.
		addModulesCounters(moduleName, 1)

		// Increment the test count in the suite.
		addSuitesCounters(suiteName, 1)

		benchmarkInfos[idx] = benchmarkInfo
	}

	// Create a new instrumented internal benchmarks
	newBenchmarkArray := make([]testing.InternalBenchmark, len(*internalBenchmarks))
	for idx, benchmarkInfo := range benchmarkInfos {
		newBenchmarkArray[idx] = testing.InternalBenchmark{
			Name: benchmarkInfo.testName,
			F:    ddm.executeInternalBenchmark(benchmarkInfo),
		}
	}

	*internalBenchmarks = newBenchmarkArray
}

// executeInternalBenchmark wraps the original benchmark function to include CI visibility instrumentation.
func (ddm *M) executeInternalBenchmark(benchmarkInfo *testingBInfo) func(*testing.B) {
	originalFunc := runtime.FuncForPC(reflect.Indirect(reflect.ValueOf(benchmarkInfo.originalFunc)).Pointer())

	settings := integrations.GetSettings()
	testIsNew := true

	// Check if the test is known
	if settings.KnownTestsEnabled {
		testIsKnown, testKnownDataOk := isKnownTest(&benchmarkInfo.commonInfo)
		testIsNew = testKnownDataOk && !testIsKnown
	} else {
		// We don't mark any test as new if the feature is disabled
		testIsNew = false
	}

	instrumentedInternalFunc := func(b *testing.B) {

		// decrement level
		pBench := getBenchmarkPrivateFields(b)
		if pBench != nil {
			pBench.AddLevel(-1)
		}

		startTime := time.Now()
		module := session.GetOrCreateModule(benchmarkInfo.moduleName, integrations.WithTestModuleStartTime(startTime))
		suite := module.GetOrCreateSuite(benchmarkInfo.suiteName, integrations.WithTestSuiteStartTime(startTime))
		test := suite.CreateTest(benchmarkInfo.testName, integrations.WithTestStartTime(startTime))
		test.SetTestFunc(originalFunc)

		// If the execution is for a new test we tag the test event as new
		if testIsNew {
			// Set the is new test tag
			test.SetTag(constants.TestIsNew, "true")
		}

		// Run the original benchmark function.
		var iPfOfB *benchmarkPrivateFields
		var recoverFunc *func(r any)
		instrumentedFunc := func(b *testing.B) {
			// Stop the timer to perform initialization and replacements.
			b.StopTimer()

			defer func() {
				if r := recover(); r != nil {
					// Handle panic if it occurs during benchmark execution.
					if recoverFunc != nil {
						fn := *recoverFunc
						fn(r)
					}
					panic(r)
				}
			}()

			// Enable allocation reporting.
			b.ReportAllocs()

			// Retrieve the private fields of the inner testing.B.
			iPfOfB = getBenchmarkPrivateFields(b)
			if iPfOfB == nil {
				panic("failed to get private fields of the inner testing.B")
			}

			// Replace the benchmark function with the original one (this must be executed only once - the first iteration[b.run1]).
			if iPfOfB.benchFunc == nil {
				panic("failed to get the original benchmark function")
			}
			*iPfOfB.benchFunc = benchmarkInfo.originalFunc

			// Get the metadata regarding the execution (in case is already created from the additional features)
			execMeta := getTestMetadata(b)
			if execMeta == nil {
				// in case there's no additional features then we create the metadata for this execution and defer the disposal
				execMeta = createTestMetadata(b, nil)
				defer deleteTestMetadata(b)
			}

			// Sets the CI Visibility test
			execMeta.test = test

			// Restart the timer and execute the original benchmark function.
			b.ResetTimer()
			b.StartTimer()
			benchmarkInfo.originalFunc(b)
		}

		setCiVisibilityBenchmarkFunc(runtime.FuncForPC(reflect.Indirect(reflect.ValueOf(instrumentedFunc)).Pointer()))
		b.Run(b.Name(), instrumentedFunc)

		endTime := time.Now()
		results := iPfOfB.result

		// Set benchmark data for CI visibility.
		test.SetBenchmarkData("duration", map[string]any{
			"run":  results.N,
			"mean": results.NsPerOp(),
		})
		test.SetBenchmarkData("memory_total_operations", map[string]any{
			"run":            results.N,
			"mean":           results.AllocsPerOp(),
			"statistics.max": results.MemAllocs,
		})
		test.SetBenchmarkData("mean_heap_allocations", map[string]any{
			"run":  results.N,
			"mean": results.AllocedBytesPerOp(),
		})
		test.SetBenchmarkData("total_heap_allocations", map[string]any{
			"run":  results.N,
			"mean": iPfOfB.result.MemBytes,
		})
		if len(results.Extra) > 0 {
			mapConverted := map[string]any{}
			for k, v := range results.Extra {
				mapConverted[k] = v
			}
			test.SetBenchmarkData("extra", mapConverted)
		}

		// Define a function to handle panic during benchmark finalization.
		panicFunc := func(r any) {
			test.SetError(integrations.WithErrorInfo("panic", fmt.Sprint(r), utils.GetStacktrace(1)))
			suite.SetTag(ext.Error, true)
			module.SetTag(ext.Error, true)
			test.Close(integrations.ResultStatusFail)
			checkModuleAndSuite(module, suite)
			integrations.ExitCiVisibility()
		}
		recoverFunc = &panicFunc

		// Normal finalization: determine the benchmark result based on its state.
		if iPfOfB.B.Failed() {
			test.SetTag(ext.Error, true)
			suite.SetTag(ext.Error, true)
			module.SetTag(ext.Error, true)
			test.Close(integrations.ResultStatusFail, integrations.WithTestFinishTime(endTime))
		} else if iPfOfB.B.Skipped() {
			test.Close(integrations.ResultStatusSkip, integrations.WithTestFinishTime(endTime))
		} else {
			test.Close(integrations.ResultStatusPass, integrations.WithTestFinishTime(endTime))
		}

		checkModuleAndSuite(module, suite)
	}
	setCiVisibilityBenchmarkFunc(originalFunc)
	setCiVisibilityBenchmarkFunc(runtime.FuncForPC(reflect.Indirect(reflect.ValueOf(instrumentedInternalFunc)).Pointer()))
	return instrumentedInternalFunc
}

// RunM runs the tests and benchmarks using CI visibility.
func RunM(m *testing.M) int {
	return (*M)(m).Run()
}

// checkModuleAndSuite checks and closes the modules and suites if all tests are executed.
func checkModuleAndSuite(module integrations.TestModule, suite integrations.TestSuite) {
	// If all tests in a suite has been executed we can close the suite
	if addSuitesCounters(suite.Name(), -1) <= 0 {
		suite.Close()
	}

	// If all tests in a module has been executed we can close the module
	if addModulesCounters(module.Name(), -1) <= 0 {
		module.Close()
	}
}

// addSuitesCounters increments the suite counters for a given suite name.
func addSuitesCounters(suiteName string, delta int) int {
	suitesCountersMutex.Lock()
	defer suitesCountersMutex.Unlock()
	nValue := suitesCounters[suiteName] + delta
	suitesCounters[suiteName] = nValue
	return nValue
}

// addModulesCounters increments the module counters for a given module name.
func addModulesCounters(moduleName string, delta int) int {
	modulesCountersMutex.Lock()
	defer modulesCountersMutex.Unlock()
	nValue := modulesCounters[moduleName] + delta
	modulesCounters[moduleName] = nValue
	return nValue
}

// isKnownTest checks if a test is a known test or a new one
func isKnownTest(testInfo *commonInfo) (isKnown bool, hasKnownData bool) {
	knownTestsData := integrations.GetKnownTests()
	if knownTestsData != nil && len(knownTestsData.Tests) > 0 {
		// Check if the test is a known test or a new one
		if knownSuites, ok := knownTestsData.Tests[testInfo.moduleName]; ok {
			if knownTests, ok := knownSuites[testInfo.suiteName]; ok {
				return slices.Contains(knownTests, testInfo.testName), true
			}
		}

		return false, true
	}

	return false, false
}

// getTestManagementData retrieves the test management data for a test
func getTestManagementData(testInfo *commonInfo) (data *net.TestManagementTestsResponseDataTestPropertiesAttributes, hasTestManagementData bool) {
	testManagementData := integrations.GetTestManagementTestsData()
	if testManagementData != nil && len(testManagementData.Modules) > 0 {
		// Check if the test is quarantined
		if module, ok := testManagementData.Modules[testInfo.moduleName]; ok {
			if suite, ok := module.Suites[testInfo.suiteName]; ok {
				if test, ok := suite.Tests[testInfo.testName]; ok {
					return &test.Properties, true
				}
			}
		}

		return nil, true
	}

	return nil, false
}

// setTestTagsFromExecutionMetadata sets the test tags from the execution metadata.
func setTestTagsFromExecutionMetadata(test integrations.Test, execMeta *testExecutionMetadata) (cancelExecution bool) {
	settings := integrations.GetSettings()

	// Set the Test Optimization test to the execution metadata
	execMeta.test = test

	// If the execution is for a new test we tag the test event as new
	if execMeta.isANewTest {
		// Set the is new test tag
		test.SetTag(constants.TestIsNew, "true")
	}

	// If the execution is for a modified test
	execMeta.isAModifiedTest = execMeta.isAModifiedTest || (settings.ImpactedTestsEnabled && test.Context().Value(constants.TestIsModified) == true)

	// If the execution is a retry we tag the test event
	if execMeta.isARetry {
		// Set the retry tag
		test.SetTag(constants.TestIsRetry, "true")

		// let's set the retry reason
		if execMeta.isAttemptToFix {
			// Set attempt_to_fix as the retry reason
			test.SetTag(constants.TestRetryReason, constants.AttemptToFixRetryReason)
		} else if execMeta.isEarlyFlakeDetectionEnabled && (execMeta.isANewTest || execMeta.isAModifiedTest) {
			// Set early_flake_detection as the retry reason
			test.SetTag(constants.TestRetryReason, constants.EarlyFlakeDetectionRetryReason)
		} else if execMeta.isFlakyTestRetriesEnabled {
			// Set auto_test_retry as the retry reason
			test.SetTag(constants.TestRetryReason, constants.AutoTestRetriesRetryReason)
		} else {
			// Set the unknown reason
			test.SetTag(constants.TestRetryReason, constants.ExternalRetryReason)
		}
	}

	// If the test is an attempt to fix we tag the test event
	if execMeta.isAttemptToFix {
		test.SetTag(constants.TestIsAttempToFix, "true")
	}

	// If the test is quarantined we tag the test event
	if execMeta.isQuarantined {
		test.SetTag(constants.TestIsQuarantined, "true")
	}

	// If the test is disabled we tag the test event
	if execMeta.isDisabled {
		test.SetTag(constants.TestIsDisabled, "true")
		if !execMeta.isAttemptToFix {
			test.Close(integrations.ResultStatusSkip, integrations.WithTestSkipReason(constants.TestDisabledSkipReason))
			return true
		}
	}

	return false
}

// instrumentChattyPrinter initializes the chatty printer for verbose output if logging is enabled.
func instrumentChattyPrinter(t *testing.T) {
	if !logs.IsEnabled() {
		// If the logs integration is not enabled, we don't need to instrument the chatty printer.
		return
	}

	// Initialize the chatty printer if not already done.
	chattyPrinterOnce.Do(func() {
		chatty = getTestChattyPrinter(t)
		// If the chatty printer is enabled, we wrap the writer to capture output.
		if chatty != nil && chatty.w != nil && *chatty.w != nil {
			*chatty.w = &customWriter{chatty: chatty, writer: *chatty.w}
		}
	})
}

// collectAndWriteLogs collects logs from the chatty printer and the test output, and writes them to the test.
func collectAndWriteLogs(t *testing.T, test integrations.Test) {
	if !logs.IsEnabled() {
		// If the logs integration is not enabled, we don't need to collect or write logs.
		return
	}

	if chatty != nil && chatty.w != nil && *chatty.w != nil {
		if writer, ok := (*chatty.w).(*customWriter); ok {
			strOutput := writer.GetOutput(test.Name())
			if len(strOutput) > 0 {
				sc := bufio.NewScanner(strings.NewReader(strOutput))
				for sc.Scan() {
					test.Log(sc.Text(), "")
				}

				// if the chatty printer has output, we skip the test output extraction
				return
			}
		}
	}

	if tCommon := getTestPrivateFields(t); tCommon != nil && tCommon.output != nil {
		strOutput := string(tCommon.GetOutput())
		if len(strOutput) > 0 {
			sc := bufio.NewScanner(strings.NewReader(strOutput))
			for sc.Scan() {
				test.Log(sc.Text(), "")
			}
		}
	}
}

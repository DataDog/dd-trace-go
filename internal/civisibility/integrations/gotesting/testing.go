// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gotesting

import (
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils"
)

const (
	// testFramework represents the name of the testing framework.
	testFramework = "golang.org/pkg/testing"
)

var (
	// session represents the CI visibility test session.
	session integrations.DdTestSession

	// testInfos holds information about the instrumented tests.
	testInfos []*testingTInfo

	// benchmarkInfos holds information about the instrumented benchmarks.
	benchmarkInfos []*testingBInfo

	// modulesCounters keeps track of the number of tests per module.
	modulesCounters = map[string]*int32{}

	// suitesCounters keeps track of the number of tests per suite.
	suitesCounters = map[string]*int32{}
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
	integrations.EnsureCiVisibilityInitialization()
	defer integrations.ExitCiVisibility()

	// Create a new test session for CI visibility.
	session = integrations.CreateTestSession()

	m := (*testing.M)(ddm)

	// Instrument the internal tests for CI visibility.
	ddm.instrumentInternalTests(getInternalTestArray(m))

	// Instrument the internal benchmarks for CI visibility.
	for _, v := range os.Args {
		// check if benchmarking is enabled to instrument
		if strings.Contains(v, "-bench") || strings.Contains(v, "test.bench") {
			ddm.instrumentInternalBenchmarks(getInternalBenchmarkArray(m))
			break
		}
	}

	// Run the tests and benchmarks.
	var exitCode = m.Run()

	// Close the session and return the exit code.
	session.Close(exitCode)
	return exitCode
}

// instrumentInternalTests instruments the internal tests for CI visibility.
func (ddm *M) instrumentInternalTests(internalTests *[]testing.InternalTest) {
	if internalTests != nil {
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

			// Initialize module and suite counters if not already present.
			if _, ok := modulesCounters[moduleName]; !ok {
				var v int32
				modulesCounters[moduleName] = &v
			}
			// Increment the test count in the module.
			atomic.AddInt32(modulesCounters[moduleName], 1)

			if _, ok := suitesCounters[suiteName]; !ok {
				var v int32
				suitesCounters[suiteName] = &v
			}
			// Increment the test count in the suite.
			atomic.AddInt32(suitesCounters[suiteName], 1)

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
}

// executeInternalTest wraps the original test function to include CI visibility instrumentation.
func (ddm *M) executeInternalTest(testInfo *testingTInfo) func(*testing.T) {
	originalFunc := runtime.FuncForPC(reflect.Indirect(reflect.ValueOf(testInfo.originalFunc)).Pointer())
	return func(t *testing.T) {
		// Create or retrieve the module, suite, and test for CI visibility.
		module := session.GetOrCreateModuleWithFramework(testInfo.moduleName, testFramework, runtime.Version())
		suite := module.GetOrCreateSuite(testInfo.suiteName)
		test := suite.CreateTest(testInfo.testName)
		test.SetTestFunc(originalFunc)
		setCiVisibilityTest(t, test)
		defer func() {
			if r := recover(); r != nil {
				// Handle panic and set error information.
				test.SetErrorInfo("panic", fmt.Sprint(r), utils.GetStacktrace(1))
				suite.SetTag(ext.Error, true)
				module.SetTag(ext.Error, true)
				test.Close(integrations.ResultStatusFail)
				checkModuleAndSuite(module, suite)
				integrations.ExitCiVisibility()
				panic(r)
			} else {
				// Normal finalization: determine the test result based on its state.
				if t.Failed() {
					test.SetTag(ext.Error, true)
					suite.SetTag(ext.Error, true)
					module.SetTag(ext.Error, true)
					test.Close(integrations.ResultStatusFail)
				} else if t.Skipped() {
					test.Close(integrations.ResultStatusSkip)
				} else {
					test.Close(integrations.ResultStatusPass)
				}

				checkModuleAndSuite(module, suite)
			}
		}()

		// Execute the original test function.
		testInfo.originalFunc(t)
	}
}

// instrumentInternalBenchmarks instruments the internal benchmarks for CI visibility.
func (ddm *M) instrumentInternalBenchmarks(internalBenchmarks *[]testing.InternalBenchmark) {
	if internalBenchmarks != nil {
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

			// Initialize module and suite counters if not already present.
			if _, ok := modulesCounters[moduleName]; !ok {
				var v int32
				modulesCounters[moduleName] = &v
			}
			// Increment the test count in the module.
			atomic.AddInt32(modulesCounters[moduleName], 1)

			if _, ok := suitesCounters[suiteName]; !ok {
				var v int32
				suitesCounters[suiteName] = &v
			}
			// Increment the test count in the suite.
			atomic.AddInt32(suitesCounters[suiteName], 1)

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
}

// executeInternalBenchmark wraps the original benchmark function to include CI visibility instrumentation.
func (ddm *M) executeInternalBenchmark(benchmarkInfo *testingBInfo) func(*testing.B) {
	return func(b *testing.B) {

		// decrement level
		getBenchmarkPrivateFields(b).AddLevel(-1)

		startTime := time.Now()
		originalFunc := runtime.FuncForPC(reflect.Indirect(reflect.ValueOf(benchmarkInfo.originalFunc)).Pointer())
		module := session.GetOrCreateModuleWithFrameworkAndStartTime(benchmarkInfo.moduleName, testFramework, runtime.Version(), startTime)
		suite := module.GetOrCreateSuiteWithStartTime(benchmarkInfo.suiteName, startTime)
		test := suite.CreateTestWithStartTime(benchmarkInfo.testName, startTime)
		test.SetTestFunc(originalFunc)

		// Run the original benchmark function.
		var iPfOfB *benchmarkPrivateFields
		var recoverFunc *func(r any)
		b.Run(b.Name(), func(b *testing.B) {
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
			// Replace the benchmark function with the original one (this must be executed only once - the first iteration[b.run1]).
			*iPfOfB.benchFunc = benchmarkInfo.originalFunc
			// Set the CI visibility benchmark.
			setCiVisibilityBenchmark(b, test)

			// Restart the timer and execute the original benchmark function.
			b.ResetTimer()
			b.StartTimer()
			benchmarkInfo.originalFunc(b)
		})

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
			test.SetErrorInfo("panic", fmt.Sprint(r), utils.GetStacktrace(1))
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
			test.CloseWithFinishTime(integrations.ResultStatusFail, endTime)
		} else if iPfOfB.B.Skipped() {
			test.CloseWithFinishTime(integrations.ResultStatusSkip, endTime)
		} else {
			test.CloseWithFinishTime(integrations.ResultStatusPass, endTime)
		}

		checkModuleAndSuite(module, suite)
	}
}

// RunM runs the tests and benchmarks using CI visibility.
func RunM(m *testing.M) int {
	return (*M)(m).Run()
}

// checkModuleAndSuite checks and closes the modules and suites if all tests are executed.
func checkModuleAndSuite(module integrations.DdTestModule, suite integrations.DdTestSuite) {
	// If all tests in a suite has been executed we can close the suite
	if atomic.AddInt32(suitesCounters[suite.Name()], -1) <= 0 {
		suite.Close()
	}

	// If all tests in a module has been executed we can close the module
	if atomic.AddInt32(modulesCounters[module.Name()], -1) <= 0 {
		module.Close()
	}
}

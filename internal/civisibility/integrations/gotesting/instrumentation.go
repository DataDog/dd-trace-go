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

// The following functions are being used by the gotesting package for manual instrumentation and the orchestrion
// automatic instrumentation

// instrumentTestingM helper function to instrument internalTests and internalBenchmarks in a `*testing.M` instance.
func instrumentTestingM(m *testing.M) func(exitCode int) {
	// Initialize CI Visibility
	integrations.EnsureCiVisibilityInitialization()

	// Create a new test session for CI visibility.
	session = integrations.CreateTestSession()

	ddm := (*M)(m)

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

	return func(exitCode int) {
		// Close the session and return the exit code.
		session.Close(exitCode)

		// Finalize CI Visibility
		integrations.ExitCiVisibility()
	}
}

// instrumentTestingTFunc helper function to instrument a testing function func(*testing.T)
func instrumentTestingTFunc(f func(*testing.T)) func(*testing.T) {
	// Reflect the function to obtain its pointer.
	fReflect := reflect.Indirect(reflect.ValueOf(f))
	moduleName, suiteName := utils.GetModuleAndSuiteName(fReflect.Pointer())
	originalFunc := runtime.FuncForPC(fReflect.Pointer())

	// Increment the test count in the module.
	atomic.AddInt32(modulesCounters[moduleName], 1)

	// Increment the test count in the suite.
	atomic.AddInt32(suitesCounters[suiteName], 1)

	return func(t *testing.T) {
		// Create or retrieve the module, suite, and test for CI visibility.
		module := session.GetOrCreateModuleWithFramework(moduleName, testFramework, runtime.Version())
		suite := module.GetOrCreateSuite(suiteName)
		test := suite.CreateTest(t.Name())
		test.SetTestFunc(originalFunc)
		setCiVisibilityTest(t, test)
		defer func() {
			if r := recover(); r != nil {
				// Handle panic and set error information.
				test.SetErrorInfo("panic", fmt.Sprint(r), utils.GetStacktrace(1))
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
		f(t)
	}
}

// instrumentTestingTSetErrorInfo helper function to set an error in the `testing.T` CI Visibility span
func instrumentTestingTSetErrorInfo(t *testing.T, errType string, errMessage string, skip int) {
	ciTest := getCiVisibilityTest(t)
	if ciTest != nil {
		ciTest.SetErrorInfo(errType, errMessage, utils.GetStacktrace(2+skip))
	}
}

// instrumentTestingTCloseAndSkip helper function to close and skip with a reason a `testing.T` CI Visibility span
func instrumentTestingTCloseAndSkip(t *testing.T, skipReason string) {
	ciTest := getCiVisibilityTest(t)
	if ciTest != nil {
		ciTest.CloseWithFinishTimeAndSkipReason(integrations.ResultStatusSkip, time.Now(), skipReason)
	}
}

// instrumentTestingTSkipNow helper function to close and skip a `testing.T` CI Visibility span
func instrumentTestingTSkipNow(t *testing.T) {
	ciTest := getCiVisibilityTest(t)
	if ciTest != nil {
		ciTest.Close(integrations.ResultStatusSkip)
	}
}

// instrumentTestingBFunc helper function to instrument a benchmark function func(*testing.B)
func instrumentTestingBFunc(pb *testing.B, name string, f func(*testing.B)) (string, func(*testing.B)) {
	// Avoid instrumenting twice
	if hasCiVisibilityBenchmarkFunc(&f) {
		return name, f
	}

	// Reflect the function to obtain its pointer.
	fReflect := reflect.Indirect(reflect.ValueOf(f))
	moduleName, suiteName := utils.GetModuleAndSuiteName(fReflect.Pointer())
	originalFunc := runtime.FuncForPC(fReflect.Pointer())

	// Increment the test count in the module.
	atomic.AddInt32(modulesCounters[moduleName], 1)

	// Increment the test count in the suite.
	atomic.AddInt32(suitesCounters[suiteName], 1)

	return subBenchmarkAutoName, func(b *testing.B) {
		// The sub-benchmark implementation relies on creating a dummy sub benchmark (called [DD:TestVisibility]) with
		// a Run over the original sub benchmark function to get the child results without interfering measurements
		// By doing this the name of the sub-benchmark are changed
		// from:
		// 		benchmark/child
		// to:
		//		benchmark/[DD:TestVisibility]/child
		// We use regex and decrement the depth level of the benchmark to restore the original name

		// Decrement level.
		bpf := getBenchmarkPrivateFields(b)
		bpf.AddLevel(-1)

		startTime := time.Now()
		module := session.GetOrCreateModuleWithFrameworkAndStartTime(moduleName, testFramework, runtime.Version(), startTime)
		suite := module.GetOrCreateSuiteWithStartTime(suiteName, startTime)
		test := suite.CreateTestWithStartTime(fmt.Sprintf("%s/%s", pb.Name(), name), startTime)
		test.SetTestFunc(originalFunc)

		// Restore the original name without the sub-benchmark auto name.
		*bpf.name = subBenchmarkAutoNameRegex.ReplaceAllString(*bpf.name, "")

		// Run original benchmark.
		var iPfOfB *benchmarkPrivateFields
		var recoverFunc *func(r any)
		instrumentedFunc := func(b *testing.B) {
			// Stop the timer to do the initialization and replacements.
			b.StopTimer()

			defer func() {
				if r := recover(); r != nil {
					if recoverFunc != nil {
						fn := *recoverFunc
						fn(r)
					}
					panic(r)
				}
			}()

			// First time we get the private fields of the inner testing.B.
			iPfOfB = getBenchmarkPrivateFields(b)
			// Replace this function with the original one (executed only once - the first iteration[b.run1]).
			*iPfOfB.benchFunc = f
			// Set b to the CI visibility test.
			setCiVisibilityBenchmark(b, test)

			// Enable the timer again.
			b.ResetTimer()
			b.StartTimer()

			// Execute original func
			f(b)
		}

		setCiVisibilityBenchmarkFunc(&instrumentedFunc)
		defer deleteCiVisibilityBenchmarkFunc(&instrumentedFunc)
		b.Run(name, instrumentedFunc)

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

// instrumentTestingBSetErrorInfo helper function to set an error in the `testing.B` CI Visibility span
func instrumentTestingBSetErrorInfo(b *testing.B, errType string, errMessage string, skip int) {
	ciTest := getCiVisibilityBenchmark(b)
	if ciTest != nil {
		ciTest.SetErrorInfo(errType, errMessage, utils.GetStacktrace(2+skip))
	}
}

// instrumentTestingBCloseAndSkip helper function to close and skip with a reason a `testing.B` CI Visibility span
func instrumentTestingBCloseAndSkip(b *testing.B, skipReason string) {
	ciTest := getCiVisibilityBenchmark(b)
	if ciTest != nil {
		ciTest.CloseWithFinishTimeAndSkipReason(integrations.ResultStatusSkip, time.Now(), skipReason)
	}
}

// instrumentTestingBSkipNow helper function to close and skip a `testing.B` CI Visibility span
func instrumentTestingBSkipNow(b *testing.B) {
	ciTest := getCiVisibilityBenchmark(b)
	if ciTest != nil {
		ciTest.Close(integrations.ResultStatusSkip)
	}
}

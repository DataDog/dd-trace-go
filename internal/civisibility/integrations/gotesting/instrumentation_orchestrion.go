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
	_ "unsafe"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/constants"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations/gotesting/coverage"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils"
)

// ******************************************************************************************************************
// WARNING: DO NOT CHANGE THE SIGNATURE OF THESE FUNCTIONS!
//
//  The following functions are being used by both the manual api and most importantly the Orchestrion automatic
//  instrumentation integration.
// ******************************************************************************************************************

// instrumentTestingM helper function to instrument internalTests and internalBenchmarks in a `*testing.M` instance.
//
//go:linkname instrumentTestingM
func instrumentTestingM(m *testing.M) func(exitCode int) {
	// Check if CI Visibility was disabled using the kill switch before trying to initialize it
	atomic.StoreInt32(&ciVisibilityEnabledValue, -1)
	if !isCiVisibilityEnabled() || !testing.Testing() {
		return func(exitCode int) {}
	}

	// Initialize CI Visibility
	integrations.EnsureCiVisibilityInitialization()

	// Create a new test session for CI visibility.
	session = integrations.CreateTestSession(integrations.WithTestSessionFramework(testFramework, runtime.Version()))

	settings := integrations.GetSettings()
	if settings != nil && settings.CodeCoverage {
		// Initialize the runtime coverage if enabled.
		coverage.InitializeCoverage(m)
	}

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
		// Check for code coverage if enabled.
		if testing.CoverMode() != "" {

			var cov float64
			// let's try first with our coverage package
			if coverage.CanCollect() {
				cov = coverage.GetCoverage()
			}
			if cov == 0 {
				// if not we try we the default testing package
				cov = testing.Coverage()
			}

			coveragePercentage := cov * 100
			session.SetTag(constants.CodeCoveragePercentageOfTotalLines, coveragePercentage)
		}

		// Close the session and return the exit code.
		session.Close(exitCode)

		// Finalize CI Visibility
		integrations.ExitCiVisibility()
	}
}

// instrumentTestingTFunc helper function to instrument a testing function func(*testing.T)
//
//go:linkname instrumentTestingTFunc
func instrumentTestingTFunc(f func(*testing.T)) func(*testing.T) {
	// Check if CI Visibility was disabled using the kill switch before instrumenting
	if !isCiVisibilityEnabled() || !testing.Testing() {
		return f
	}

	// Reflect the function to obtain its pointer.
	fReflect := reflect.Indirect(reflect.ValueOf(f))
	moduleName, suiteName := utils.GetModuleAndSuiteName(fReflect.Pointer())
	originalFunc := runtime.FuncForPC(fReflect.Pointer())

	// Avoid instrumenting twice
	metadata := getInstrumentationMetadata(originalFunc)
	if metadata != nil && metadata.IsInternal {
		// If is an internal test, we don't instrument because f is already the instrumented func by executeInternalTest
		return f
	}

	instrumentedFn := func(t *testing.T) {
		// Initialize module counters if not already present.
		if _, ok := modulesCounters[moduleName]; !ok {
			var v int32
			modulesCounters[moduleName] = &v
		}
		// Increment the test count in the module.
		atomic.AddInt32(modulesCounters[moduleName], 1)

		// Initialize suite counters if not already present.
		if _, ok := suitesCounters[suiteName]; !ok {
			var v int32
			suitesCounters[suiteName] = &v
		}
		// Increment the test count in the suite.
		atomic.AddInt32(suitesCounters[suiteName], 1)

		// Create or retrieve the module, suite, and test for CI visibility.
		module := session.GetOrCreateModule(moduleName)
		suite := module.GetOrCreateSuite(suiteName)
		test := suite.CreateTest(t.Name())
		test.SetTestFunc(originalFunc)

		// Get the metadata regarding the execution (in case is already created from the additional features)
		execMeta := getTestMetadata(t)
		if execMeta == nil {
			// in case there's no additional features then we create the metadata for this execution and defer the disposal
			execMeta = createTestMetadata(t)
			defer deleteTestMetadata(t)
		}

		// Because this is a subtest let's propagate some execution metadata from the parent test
		testPrivateFields := getTestPrivateFields(t)
		if testPrivateFields != nil && testPrivateFields.parent != nil {
			parentExecMeta := getTestMetadataFromPointer(*testPrivateFields.parent)
			if parentExecMeta != nil {
				if parentExecMeta.isANewTest {
					execMeta.isANewTest = true
				}
				if parentExecMeta.isARetry {
					execMeta.isARetry = true
				}
			}
		}

		// Set the CI visibility test.
		execMeta.test = test

		// If the execution is for a new test we tag the test event from early flake detection
		if execMeta.isANewTest {
			// Set the is new test tag
			test.SetTag(constants.TestIsNew, "true")
		}

		// If the execution is a retry we tag the test event
		if execMeta.isARetry {
			// Set the retry tag
			test.SetTag(constants.TestIsRetry, "true")
		}

		defer func() {
			if r := recover(); r != nil {
				// Handle panic and set error information.
				test.SetError(integrations.WithErrorInfo("panic", fmt.Sprint(r), utils.GetStacktrace(1)))
				test.Close(integrations.ResultStatusFail)
				checkModuleAndSuite(module, suite)
				// this is not an internal test. Retries are not applied to subtest (because the parent internal test is going to be retried)
				// so for this case we avoid closing CI Visibility, but we don't stop the panic from happening.
				// it will be handled by `t.Run`
				if checkIfCIVisibilityExitIsRequiredByPanic() {
					integrations.ExitCiVisibility()
				}
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

	setInstrumentationMetadata(runtime.FuncForPC(reflect.Indirect(reflect.ValueOf(instrumentedFn)).Pointer()), &instrumentationMetadata{IsInternal: true})
	return instrumentedFn
}

// instrumentSetErrorInfo helper function to set an error in the `*testing.T, *testing.B, *testing.common` CI Visibility span
//
//go:linkname instrumentSetErrorInfo
func instrumentSetErrorInfo(tb testing.TB, errType string, errMessage string, skip int) {
	// Check if CI Visibility was disabled using the kill switch before
	if !isCiVisibilityEnabled() {
		return
	}

	// Get the CI Visibility span and check if we can set the error type, message and stack
	ciTestItem := getTestMetadata(tb)
	if ciTestItem != nil && ciTestItem.test != nil && ciTestItem.error.CompareAndSwap(0, 1) {
		ciTestItem.test.SetError(integrations.WithErrorInfo(errType, errMessage, utils.GetStacktrace(2+skip)))
	}
}

// instrumentCloseAndSkip helper function to close and skip with a reason a `*testing.T, *testing.B, *testing.common` CI Visibility span
//
//go:linkname instrumentCloseAndSkip
func instrumentCloseAndSkip(tb testing.TB, skipReason string) {
	// Check if CI Visibility was disabled using the kill switch before
	if !isCiVisibilityEnabled() {
		return
	}

	// Get the CI Visibility span and check if we can mark it as skipped and close it
	ciTestItem := getTestMetadata(tb)
	if ciTestItem != nil && ciTestItem.test != nil && ciTestItem.skipped.CompareAndSwap(0, 1) {
		ciTestItem.test.Close(integrations.ResultStatusSkip, integrations.WithTestSkipReason(skipReason))
	}
}

// instrumentSkipNow helper function to close and skip a `*testing.T, *testing.B, *testing.common` CI Visibility span
//
//go:linkname instrumentSkipNow
func instrumentSkipNow(tb testing.TB) {
	// Check if CI Visibility was disabled using the kill switch before
	if !isCiVisibilityEnabled() {
		return
	}

	// Get the CI Visibility span and check if we can mark it as skipped and close it
	ciTestItem := getTestMetadata(tb)
	if ciTestItem != nil && ciTestItem.test != nil && ciTestItem.skipped.CompareAndSwap(0, 1) {
		ciTestItem.test.Close(integrations.ResultStatusSkip)
	}
}

// instrumentTestingBFunc helper function to instrument a benchmark function func(*testing.B)
//
//go:linkname instrumentTestingBFunc
func instrumentTestingBFunc(pb *testing.B, name string, f func(*testing.B)) (string, func(*testing.B)) {
	// Check if CI Visibility was disabled using the kill switch before instrumenting
	if !isCiVisibilityEnabled() {
		return name, f
	}

	// Reflect the function to obtain its pointer.
	fReflect := reflect.Indirect(reflect.ValueOf(f))
	moduleName, suiteName := utils.GetModuleAndSuiteName(fReflect.Pointer())
	originalFunc := runtime.FuncForPC(fReflect.Pointer())

	// Avoid instrumenting twice
	if hasCiVisibilityBenchmarkFunc(originalFunc) {
		return name, f
	}

	instrumentedFunc := func(b *testing.B) {
		// The sub-benchmark implementation relies on creating a dummy sub benchmark (called [DD:TestVisibility]) with
		// a Run over the original sub benchmark function to get the child results without interfering measurements
		// By doing this the name of the sub-benchmark are changed
		// from:
		// 		benchmark/child
		// to:
		//		benchmark/[DD:TestVisibility]/child
		// We use regex and decrement the depth level of the benchmark to restore the original name

		// Initialize module counters if not already present.
		if _, ok := modulesCounters[moduleName]; !ok {
			var v int32
			modulesCounters[moduleName] = &v
		}
		// Increment the test count in the module.
		atomic.AddInt32(modulesCounters[moduleName], 1)

		// Initialize suite counters if not already present.
		if _, ok := suitesCounters[suiteName]; !ok {
			var v int32
			suitesCounters[suiteName] = &v
		}
		// Increment the test count in the suite.
		atomic.AddInt32(suitesCounters[suiteName], 1)

		// Decrement level.
		bpf := getBenchmarkPrivateFields(b)
		if bpf == nil {
			panic("error getting private fields of the benchmark")
		}
		bpf.AddLevel(-1)

		startTime := time.Now()
		module := session.GetOrCreateModule(moduleName, integrations.WithTestModuleStartTime(startTime))
		suite := module.GetOrCreateSuite(suiteName, integrations.WithTestSuiteStartTime(startTime))
		test := suite.CreateTest(fmt.Sprintf("%s/%s", pb.Name(), name), integrations.WithTestStartTime(startTime))
		test.SetTestFunc(originalFunc)

		// Restore the original name without the sub-benchmark auto name.
		if bpf.name != nil {
			*bpf.name = subBenchmarkAutoNameRegex.ReplaceAllString(*bpf.name, "")
		}

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
			if iPfOfB == nil {
				panic("error getting private fields of the benchmark")
			}

			// Replace this function with the original one (executed only once - the first iteration[b.run1]).
			if iPfOfB.benchFunc == nil {
				panic("error getting the benchmark function")
			}
			*iPfOfB.benchFunc = f

			// Get the metadata regarding the execution (in case is already created from the additional features)
			execMeta := getTestMetadata(b)
			if execMeta == nil {
				// in case there's no additional features then we create the metadata for this execution and defer the disposal
				execMeta = createTestMetadata(b)
				defer deleteTestMetadata(b)
			}

			// Set the CI visibility test.
			execMeta.test = test

			// Enable the timer again.
			b.ResetTimer()
			b.StartTimer()

			// Execute original func
			f(b)
		}

		setCiVisibilityBenchmarkFunc(runtime.FuncForPC(reflect.Indirect(reflect.ValueOf(instrumentedFunc)).Pointer()))
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
	setCiVisibilityBenchmarkFunc(runtime.FuncForPC(reflect.Indirect(reflect.ValueOf(instrumentedFunc)).Pointer()))
	return subBenchmarkAutoName, instrumentedFunc
}

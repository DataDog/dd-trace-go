// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gotesting

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	_ "unsafe" // required blank import to run orchestrion

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting/coverage"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
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
		return func(_ int) {}
	}

	log.Debug("instrumentTestingM: initializing CI Visibility for testing.M")

	// Initialize CI Visibility
	integrations.EnsureCiVisibilityInitialization()

	// Create a new test session for CI visibility.
	session = integrations.CreateTestSession(integrations.WithTestSessionFramework(testFramework, runtime.Version()))

	coverageInitialized := false
	settings := integrations.GetSettings()
	if settings != nil {
		if settings.CodeCoverage {
			// Initialize the runtime coverage if enabled.
			coverage.InitializeCoverage(m)
			coverageInitialized = true
		}
		if settings.TestManagement.Enabled && internal.BoolEnv(constants.CIVisibilityTestManagementEnabledEnvironmentVariable, true) {
			// Set the test management tag if enabled.
			session.SetTag(constants.TestManagementEnabled, "true")
		}
	}

	// Check if the coverage was enabled by not initialized
	if !coverageInitialized && testing.CoverMode() != "" {
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
		log.Debug("instrumentTestingM: finished with exit code: %d", exitCode)

		// Check for code coverage if enabled.
		if testing.CoverMode() != "" {
			// let's try first with our coverage package
			cov := coverage.GetCoverage()
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

	log.Debug("instrumentTestingTFunc: instrumenting test function")

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
		// Check if we have testify suite data related to this test
		testifyData := getTestifyTest(t)
		if testifyData != nil {
			// If we have testify data, we need to extract the module and suite name from the testify suite
			moduleName = testifyData.moduleName
			suiteName = testifyData.suiteName
		}

		// Increment the test count in the module.
		addModulesCounters(moduleName, 1)

		// Increment the test count in the suite.
		addSuitesCounters(suiteName, 1)

		// Create or retrieve the module, suite, and test for CI visibility.
		module := session.GetOrCreateModule(moduleName)
		suite := module.GetOrCreateSuite(suiteName)
		test := suite.CreateTest(t.Name())

		// If we have testify data we use the method function from testify so the test source is properly set
		if testifyData != nil {
			test.SetTestFunc(testifyData.methodFunc)
		} else {
			// If not, let's set the original function
			test.SetTestFunc(originalFunc)
		}

		// Get the metadata regarding the execution (in case is already created from the additional features)
		execMeta := getTestMetadata(t)
		if execMeta == nil {
			// in case there's no additional features then we create the metadata for this execution and defer the disposal
			execMeta = createTestMetadata(t, nil)
			defer deleteTestMetadata(t)
		}

		// Because this is a subtest let's propagate some execution metadata from the parent test
		testPrivateFields := getTestPrivateFields(t)
		if testPrivateFields != nil && testPrivateFields.parent != nil {
			parentExecMeta := getTestMetadataFromPointer(*testPrivateFields.parent)
			propagateTestExecutionMetadataFlags(execMeta, parentExecMeta)
		}

		// Set some required tags from the execution metadata
		cancelExecution := setTestTagsFromExecutionMetadata(test, execMeta)
		if cancelExecution {
			checkModuleAndSuite(module, suite)
			return
		}

		defer func() {
			// Collect and write logs
			collectAndWriteLogs(t, test)

			if r := recover(); r != nil {
				// Handle panic and set error information.
				if execMeta.isARetry && execMeta.isLastRetry {
					if execMeta.allRetriesFailed {
						test.SetTag(constants.TestHasFailedAllRetries, "true")
					}
					if execMeta.isAttemptToFix {
						test.SetTag(constants.TestAttemptToFixPassed, "false")
					}
				}
				test.SetError(integrations.WithErrorInfo("panic", fmt.Sprint(r), utils.GetStacktrace(1)))
				test.Close(integrations.ResultStatusFail)
				checkModuleAndSuite(module, suite)
				// this is not an internal test. Retries are not applied to subtest (because the parent internal test is going to be retried)
				// so for this case we avoid closing CI Visibility, but we don't stop the panic from happening.
				// it will be handled by `t.Run`
				if checkIfCIVisibilityExitIsRequiredByPanic() && !execMeta.isAttemptToFix {
					integrations.ExitCiVisibility()
				}
				panic(r)
			}
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
			checkModuleAndSuite(module, suite)
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
		log.Debug("instrumentSetErrorInfo: setting error info [name: %q, type: %q, message: %q]", ciTestItem.test.Name(), errType, errMessage)
		ciTestItem.test.SetError(integrations.WithErrorInfo(errType, errMessage, utils.GetStacktrace(2+skip)))

		// Ensure to close the test with error before CI visibility exits. In CI visibility mode, we try to never lose data.
		// If the test gets closed sooner (perhaps with another status), then this will be a noop call
		integrations.PushCiVisibilityCloseAction(func() {
			ciTestItem.test.Close(integrations.ResultStatusFail)
		})
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
		log.Debug("instrumentCloseAndSkip: skipping test [name: %q, reason: %q]", ciTestItem.test.Name(), skipReason)
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
		log.Debug("instrumentSkipNow: skipping test [name: %q]", ciTestItem.test.Name())
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

	log.Debug("instrumentTestingBFunc: instrumenting benchmark function [name: %q]", name)

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

		// Increment the test count in the module.
		addModulesCounters(moduleName, 1)

		// Increment the test count in the suite.
		addSuitesCounters(suiteName, 1)

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
				execMeta = createTestMetadata(b, nil)
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

// instrumentTestifySuiteRun helper function to instrument the testify Suite.Run function
//
//go:linkname instrumentTestifySuiteRun
func instrumentTestifySuiteRun(t *testing.T, suite any) {
	log.Debug("instrumentTestifySuiteRun: instrumenting testify suite run")
	registerTestifySuite(t, suite)
}

// getTestOptimizationContext helper function to get the context of the test
//
//go:linkname getTestOptimizationContext
func getTestOptimizationContext(tb testing.TB) context.Context {
	if iTest := getTestOptimizationTest(tb); iTest != nil {
		log.Debug("getTestOptimizationContext: returning context from test")
		return iTest.Context()
	}

	return context.Background()
}

// getTestOptimizationTest helper function to get the test optimization test of the testing.TB
//
//go:linkname getTestOptimizationTest
func getTestOptimizationTest(tb testing.TB) integrations.Test {
	ciTestItem := getTestMetadata(tb)
	if ciTestItem != nil && ciTestItem.test != nil {
		log.Debug("getTestOptimizationTest: returning test from metadata")
		return ciTestItem.test
	}

	return nil
}

// instrumentTestingParallel helper function to instrument the Parallel method of a `*testing.T` instance
//
//go:linkname instrumentTestingParallel
func instrumentTestingParallel(t *testing.T) bool {
	// Check if CI Visibility was disabled using the kill switch before
	if !isCiVisibilityEnabled() {
		return false
	}

	meta := getTestMetadata(t)
	if meta != nil && meta.originalTest != nil {
		// if we have an original test, we call parallel on it
		log.Debug("instrumentTestingParallel: calling Parallel on original test")
		meta.originalTest.Parallel()
		return true
	}

	return false
}

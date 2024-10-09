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
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/constants"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils"
)

// The following functions are being used by the gotesting package for manual instrumentation and the orchestrion
// automatic instrumentation

type (
	// instrumentationMetadata contains the internal instrumentation metadata
	instrumentationMetadata struct {
		IsInternal bool
	}

	// testExecutionMetadata contains metadata regarding an unique *testing.T or *testing.B execution
	testExecutionMetadata struct {
		test                        integrations.DdTest // internal CI Visibility test event
		error                       atomic.Int32        // flag to check if the test event has error data already
		skipped                     atomic.Int32        // flag to check if the test event has skipped data already
		panicData                   any                 // panic data recovered from an internal test execution when using an additional feature wrapper
		panicStacktrace             string              // stacktrace from the panic recovered from an internal test
		isARetry                    bool                // flag to tag if a current test execution is a retry
		isANewTest                  bool                // flag to tag if a current test execution is part of a new test (EFD not known test)
		hasAdditionalFeatureWrapper bool                // flag to check if the current execution is part of an additional feature wrapper
	}
)

var (
	// ciVisibilityEnabledValue holds a value to check if ci visibility is enabled or not (1 = enabled / 0 = disabled)
	ciVisibilityEnabledValue int32 = -1

	// instrumentationMap holds a map of *runtime.Func for tracking instrumented functions
	instrumentationMap = map[*runtime.Func]*instrumentationMetadata{}

	// instrumentationMapMutex is a read-write mutex for synchronizing access to instrumentationMap.
	instrumentationMapMutex sync.RWMutex

	// ciVisibilityTests holds a map of *testing.T or *testing.B to execution metadata for tracking tests.
	ciVisibilityTestMetadata = map[unsafe.Pointer]*testExecutionMetadata{}

	// ciVisibilityTestMetadataMutex is a read-write mutex for synchronizing access to ciVisibilityTestMetadata.
	ciVisibilityTestMetadataMutex sync.RWMutex
)

// isCiVisibilityEnabled gets if CI Visibility has been enabled or disabled by the "DD_CIVISIBILITY_ENABLED" environment variable
func isCiVisibilityEnabled() bool {
	// let's check if the value has already been loaded from the env-vars
	enabledValue := atomic.LoadInt32(&ciVisibilityEnabledValue)
	if enabledValue == -1 {
		// Get the DD_CIVISIBILITY_ENABLED env var, if not present we default to false (for now). This is because if we are here, it means
		// that the process was instrumented for ci visibility or by using orchestrion.
		// So effectively this env-var will act as a kill switch for cases where the code is instrumented, but
		// we don't want the civisibility instrumentation to be enabled.
		// *** For preview releases we will default to false, meaning that the use of ci visibility must be opt-in ***
		if internal.BoolEnv(constants.CIVisibilityEnabledEnvironmentVariable, false) {
			atomic.StoreInt32(&ciVisibilityEnabledValue, 1)
			return true
		} else {
			atomic.StoreInt32(&ciVisibilityEnabledValue, 0)
			return false
		}
	}

	return enabledValue == 1
}

// getInstrumentationMetadata gets the stored instrumentation metadata for a given *runtime.Func.
func getInstrumentationMetadata(fn *runtime.Func) *instrumentationMetadata {
	instrumentationMapMutex.RLock()
	defer instrumentationMapMutex.RUnlock()
	if v, ok := instrumentationMap[fn]; ok {
		return v
	}
	return nil
}

// setInstrumentationMetadata stores an instrumentation metadata for a given *runtime.Func.
func setInstrumentationMetadata(fn *runtime.Func, metadata *instrumentationMetadata) {
	instrumentationMapMutex.RLock()
	defer instrumentationMapMutex.RUnlock()
	instrumentationMap[fn] = metadata
}

// createTestMetadata creates the CI visibility test metadata associated with a given *testing.T, *testing.B, *testing.common
func createTestMetadata(tb testing.TB) *testExecutionMetadata {
	ciVisibilityTestMetadataMutex.RLock()
	defer ciVisibilityTestMetadataMutex.RUnlock()
	execMetadata := &testExecutionMetadata{}
	ciVisibilityTestMetadata[reflect.ValueOf(tb).UnsafePointer()] = execMetadata
	return execMetadata
}

// getTestMetadata retrieves the CI visibility test metadata associated with a given *testing.T, *testing.B, *testing.common
func getTestMetadata(tb testing.TB) *testExecutionMetadata {
	return getTestMetadataFromPointer(reflect.ValueOf(tb).UnsafePointer())
}

// getTestMetadataFromPointer retrieves the CI visibility test metadata associated with a given *testing.T, *testing.B, *testing.common using a pointer
func getTestMetadataFromPointer(ptr unsafe.Pointer) *testExecutionMetadata {
	ciVisibilityTestMetadataMutex.RLock()
	defer ciVisibilityTestMetadataMutex.RUnlock()
	if v, ok := ciVisibilityTestMetadata[ptr]; ok {
		return v
	}
	return nil
}

// deleteTestMetadata delete the CI visibility test metadata associated with a given *testing.T, *testing.B, *testing.common
func deleteTestMetadata(tb testing.TB) {
	ciVisibilityTestMetadataMutex.RLock()
	defer ciVisibilityTestMetadataMutex.RUnlock()
	delete(ciVisibilityTestMetadata, reflect.ValueOf(tb).UnsafePointer())
}

// instrumentTestingM helper function to instrument internalTests and internalBenchmarks in a `*testing.M` instance.
func instrumentTestingM(m *testing.M) func(exitCode int) {
	// Check if CI Visibility was disabled using the kill switch before trying to initialize it
	atomic.StoreInt32(&ciVisibilityEnabledValue, -1)
	if !isCiVisibilityEnabled() {
		return func(exitCode int) {}
	}

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
		// Check for code coverage if enabled.
		if testing.CoverMode() != "" {
			coveragePercentage := testing.Coverage() * 100
			session.SetTag(constants.CodeCoveragePercentageOfTotalLines, coveragePercentage)
		}

		// Close the session and return the exit code.
		session.Close(exitCode)

		// Finalize CI Visibility
		integrations.ExitCiVisibility()
	}
}

// instrumentTestingTFunc helper function to instrument a testing function func(*testing.T)
func instrumentTestingTFunc(f func(*testing.T)) func(*testing.T) {
	// Check if CI Visibility was disabled using the kill switch before instrumenting
	if !isCiVisibilityEnabled() {
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
		module := session.GetOrCreateModuleWithFramework(moduleName, testFramework, runtime.Version())
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
		if testPrivateFields.parent != nil {
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
				test.SetErrorInfo("panic", fmt.Sprint(r), utils.GetStacktrace(1))
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
func instrumentSetErrorInfo(tb testing.TB, errType string, errMessage string, skip int) {
	// Check if CI Visibility was disabled using the kill switch before
	if !isCiVisibilityEnabled() {
		return
	}

	// Get the CI Visibility span and check if we can set the error type, message and stack
	ciTestItem := getTestMetadata(tb)
	if ciTestItem != nil && ciTestItem.test != nil && ciTestItem.error.CompareAndSwap(0, 1) {
		ciTestItem.test.SetErrorInfo(errType, errMessage, utils.GetStacktrace(2+skip))
	}
}

// instrumentCloseAndSkip helper function to close and skip with a reason a `*testing.T, *testing.B, *testing.common` CI Visibility span
func instrumentCloseAndSkip(tb testing.TB, skipReason string) {
	// Check if CI Visibility was disabled using the kill switch before
	if !isCiVisibilityEnabled() {
		return
	}

	// Get the CI Visibility span and check if we can mark it as skipped and close it
	ciTestItem := getTestMetadata(tb)
	if ciTestItem != nil && ciTestItem.test != nil && ciTestItem.skipped.CompareAndSwap(0, 1) {
		ciTestItem.test.CloseWithFinishTimeAndSkipReason(integrations.ResultStatusSkip, time.Now(), skipReason)
	}
}

// instrumentSkipNow helper function to close and skip a `*testing.T, *testing.B, *testing.common` CI Visibility span
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
	setCiVisibilityBenchmarkFunc(originalFunc)
	setCiVisibilityBenchmarkFunc(runtime.FuncForPC(reflect.Indirect(reflect.ValueOf(instrumentedFunc)).Pointer()))
	return subBenchmarkAutoName, instrumentedFunc
}

// checkIfCIVisibilityExitIsRequiredByPanic checks the additional features settings to decide if we allow individual tests to panic or not
func checkIfCIVisibilityExitIsRequiredByPanic() bool {
	// Apply additional features
	settings := integrations.GetSettings()

	// If we don't plan to do retries then we allow to panic
	return !settings.FlakyTestRetriesEnabled && !settings.EarlyFlakeDetection.Enabled
}

// applyAdditionalFeaturesToTestFunc applies all the additional features as wrapper of a func(*testing.T)
func applyAdditionalFeaturesToTestFunc(f func(*testing.T), testInfo *commonInfo) func(*testing.T) {
	// Apply additional features
	settings := integrations.GetSettings()

	// Wrapper function
	wrapperFunc := &f

	// Flaky test retries
	if settings.FlakyTestRetriesEnabled {
		flakyRetrySettings := integrations.GetFlakyRetriesSettings()

		// if the retry count per test is > 1 and if we still have remaining total retry count
		if flakyRetrySettings.RetryCount > 1 && flakyRetrySettings.RemainingTotalRetryCount > 0 {
			targetFunc := f
			flakyRetryFunc := func(t *testing.T) {
				retryCount := flakyRetrySettings.RetryCount
				executionIndex := -1
				var panicExecution *testExecutionMetadata

				// Get the private fields from the *testing.T instance
				tParentCommonPrivates := getTestParentPrivateFields(t)

				// Module and suite for this test
				var module integrations.DdTestModule
				var suite integrations.DdTestSuite

				// Check if we have execution metadata to propagate
				originalExecMeta := getTestMetadata(t)

				for {
					// let's clear the matcher subnames map before any execution to avoid subname tests to be called "parent/subname#NN" due the retries
					getTestContextMatcherPrivateFields(t).ClearSubNames()

					// increment execution index
					executionIndex++

					// we need to create a new local copy of `t` as a way to isolate the results of this execution.
					// this is because we don't want these executions to affect the overall result of the test process
					// nor the parent test status.
					ptrToLocalT := &testing.T{}
					copyTestWithoutParent(t, ptrToLocalT)

					// we create a dummy parent so we can run the test using this local copy
					// without affecting the test parent
					localTPrivateFields := getTestPrivateFields(ptrToLocalT)
					*localTPrivateFields.parent = unsafe.Pointer(&testing.T{})

					// create an execution metadata instance
					execMeta := createTestMetadata(ptrToLocalT)
					execMeta.hasAdditionalFeatureWrapper = true

					// propagate set tags from a parent wrapper
					if originalExecMeta != nil {
						if originalExecMeta.isANewTest {
							execMeta.isANewTest = true
						}
						if originalExecMeta.isARetry {
							execMeta.isARetry = true
						}
					}

					// if we are in a retry execution we set the `isARetry` flag so we can tag the test event.
					if executionIndex > 0 {
						execMeta.isARetry = true
					}

					// run original func similar to it gets run internally in tRunner
					chn := make(chan struct{}, 1)
					go func() {
						defer func() {
							chn <- struct{}{}
						}()
						targetFunc(ptrToLocalT)
					}()
					<-chn

					// we call the cleanup funcs after this execution before trying another execution
					callTestCleanupPanicValue := testingTRunCleanup(ptrToLocalT, 1)
					if callTestCleanupPanicValue != nil {
						fmt.Printf("cleanup error: %v\n", callTestCleanupPanicValue)
					}

					// copy the current test to the wrapper
					if originalExecMeta != nil {
						originalExecMeta.test = execMeta.test
					}

					// extract module and suite if present
					currentSuite := execMeta.test.Suite()
					if suite == nil && currentSuite != nil {
						suite = currentSuite
					}
					if module == nil && currentSuite != nil && currentSuite.Module() != nil {
						module = currentSuite.Module()
					}

					// remove execution metadata
					deleteTestMetadata(ptrToLocalT)

					// decrement retry counts
					remainingRetries := atomic.AddInt64(&retryCount, -1)
					remainingTotalRetries := atomic.AddInt64(&flakyRetrySettings.RemainingTotalRetryCount, -1)

					// if a panic occurs we fail the test
					if execMeta.panicData != nil {
						ptrToLocalT.Fail()

						// stores the first panic data so we can do a panic later after all retries
						if panicExecution == nil {
							panicExecution = execMeta
						}
					}

					// if not failed and if there's no panic data then we don't do any retry
					// if there's no more retries we also exit the loop
					if !ptrToLocalT.Failed() || remainingRetries < 0 || remainingTotalRetries < 0 {
						// because we are not going to do any other retry we set the original `t` with the results
						// and in case of failure we mark the parent test as failed as well.
						tCommonPrivates := getTestPrivateFields(t)
						tCommonPrivates.SetFailed(ptrToLocalT.Failed())
						tCommonPrivates.SetSkipped(ptrToLocalT.Skipped())

						// Only change the parent status to failing if the current test failed
						if ptrToLocalT.Failed() {
							tParentCommonPrivates.SetFailed(ptrToLocalT.Failed())
						}
						break
					}
				}

				// in case we execute some retries then let's print a summary of the result with the retries count
				retries := flakyRetrySettings.RetryCount - (retryCount + 1)
				if retries > 0 {
					status := "passed"
					if t.Failed() {
						status = "failed"
					} else if t.Skipped() {
						status = "skipped"
					}

					fmt.Printf("    [ %v after %v retries by Datadog's auto test retries ]\n", status, retries)
				}

				if originalExecMeta == nil {
					// after all test executions we check if we need to close the suite and the module
					checkModuleAndSuite(module, suite)
				}

				// let's check if total retry count was exceeded
				if flakyRetrySettings.RemainingTotalRetryCount < 1 {
					fmt.Println("    the maximum number of total retries was exceeded.")
				}

				// if the test failed, and we have a panic information let's re-panic that
				if t.Failed() && panicExecution != nil {
					// we are about to panic, let's ensure we flush all ci visibility data and close the session event
					integrations.ExitCiVisibility()
					panic(fmt.Sprintf("test failed and panicked after %d retries.\n%v\n%v", executionIndex, panicExecution.panicData, panicExecution.panicStacktrace))
				}
			}
			wrapperFunc = &flakyRetryFunc
		}
	}

	// Early flake detection
	earlyFlakeDetectionData := integrations.GetEarlyFlakeDetectionSettings()
	if settings.EarlyFlakeDetection.Enabled &&
		earlyFlakeDetectionData != nil &&
		len(earlyFlakeDetectionData.Tests) > 0 {
		// Define is a known test flag
		isAKnownTest := false

		// Check if the test is a known test or a new one
		if knownSuites, ok := earlyFlakeDetectionData.Tests[testInfo.moduleName]; ok {
			if knownTests, ok := knownSuites[testInfo.suiteName]; ok {
				if slices.Contains(knownTests, testInfo.testName) {
					isAKnownTest = true
				}
			}
		}

		// If is a new test then we apply the EFD wrapper
		if !isAKnownTest {
			targetFunc := *wrapperFunc
			efdRetryFunc := func(t *testing.T) {
				var remainingRetriesCount int64 = 0
				executionIndex := -1
				testPassCount := 0
				testSkipCount := 0
				testFailCount := 0
				var panicExecution *testExecutionMetadata

				// Get the private fields from the *testing.T instance
				tParentCommonPrivates := getTestParentPrivateFields(t)

				// Module and suite for this test
				var module integrations.DdTestModule
				var suite integrations.DdTestSuite

				for {
					// let's clear the matcher subnames map before any execution to avoid subname tests to be called "parent/subname#NN" due the retries
					getTestContextMatcherPrivateFields(t).ClearSubNames()

					// increment execution index
					executionIndex++

					// we need to create a new local copy of `t` as a way to isolate the results of this execution.
					// this is because we don't want these executions to affect the overall result of the test process
					// nor the parent test status.
					ptrToLocalT := &testing.T{}
					copyTestWithoutParent(t, ptrToLocalT)

					// we create a dummy parent so we can run the test using this local copy
					// without affecting the test parent
					localTPrivateFields := getTestPrivateFields(ptrToLocalT)
					*localTPrivateFields.parent = unsafe.Pointer(&testing.T{})

					// create an execution metadata instance
					execMeta := createTestMetadata(ptrToLocalT)
					execMeta.hasAdditionalFeatureWrapper = true

					// set the flag new test to true
					execMeta.isANewTest = true

					// if we are in a retry execution we set the `isARetry` flag so we can tag the test event.
					if executionIndex > 0 {
						execMeta.isARetry = true
					}

					// run original func similar to it gets run internally in tRunner
					startTime := time.Now()
					chn := make(chan struct{}, 1)
					go func() {
						defer func() {
							chn <- struct{}{}
						}()
						targetFunc(ptrToLocalT)
					}()
					<-chn
					duration := time.Since(startTime)

					// we call the cleanup funcs after this execution before trying another execution
					callTestCleanupPanicValue := testingTRunCleanup(ptrToLocalT, 1)
					if callTestCleanupPanicValue != nil {
						fmt.Printf("cleanup error: %v\n", callTestCleanupPanicValue)
					}

					// extract module and suite if present
					currentSuite := execMeta.test.Suite()
					if suite == nil && currentSuite != nil {
						suite = currentSuite
					}
					if module == nil && currentSuite != nil && currentSuite.Module() != nil {
						module = currentSuite.Module()
					}

					// If this is the first execution...
					if executionIndex == 0 {
						slowTestRetriesSettings := settings.EarlyFlakeDetection.SlowTestRetries
						// adapt the number of retries depending on the duration of the test
						durationInSecs := duration.Seconds()
						if durationInSecs < 5 {
							atomic.StoreInt64(&remainingRetriesCount, (int64)(slowTestRetriesSettings.FiveS))
						} else if durationInSecs < 10 {
							atomic.StoreInt64(&remainingRetriesCount, (int64)(slowTestRetriesSettings.TenS))
						} else if durationInSecs < 30 {
							atomic.StoreInt64(&remainingRetriesCount, (int64)(slowTestRetriesSettings.ThirtyS))
						} else if duration.Minutes() < 5 {
							atomic.StoreInt64(&remainingRetriesCount, (int64)(slowTestRetriesSettings.FiveM))
						} else {
							atomic.StoreInt64(&remainingRetriesCount, 0)
						}
					}

					// remove execution metadata
					deleteTestMetadata(ptrToLocalT)

					// decrement retry counts
					remainingRetries := atomic.AddInt64(&remainingRetriesCount, -1)

					// if a panic occurs we fail the test
					if execMeta.panicData != nil {
						ptrToLocalT.Fail()

						// stores the first panic data so we can do a panic later after all retries
						if panicExecution == nil {
							panicExecution = execMeta
						}
					}

					// update the counters depending on the test result
					if ptrToLocalT.Failed() {
						testFailCount++
					} else if ptrToLocalT.Skipped() {
						testSkipCount++
					} else {
						testPassCount++
					}

					// if there's no more retries we exit the loop
					if remainingRetries < 0 {
						break
					}
				}

				// we set the original `t` with the results
				tCommonPrivates := getTestPrivateFields(t)
				status := "passed"
				if testPassCount == 0 {
					if testSkipCount > 0 {
						status = "skipped"
						tCommonPrivates.SetSkipped(true)
					}
					if testFailCount > 0 {
						status = "failed"
						tCommonPrivates.SetFailed(true)
						tParentCommonPrivates.SetFailed(true)
					}
				}

				// in case we execute some retries then let's print a summary of the result with the retries count
				if executionIndex > 0 {
					fmt.Printf("  [ %v after %v retries by Datadog's early flake detection ]\n", status, executionIndex)
				}

				// after all test executions we check if we need to close the suite and the module
				checkModuleAndSuite(module, suite)

				// if the test failed, and we have a panic information let's re-panic that
				if t.Failed() && panicExecution != nil {
					// we are about to panic, let's ensure we flush all ci visibility data and close the session event
					integrations.ExitCiVisibility()
					panic(fmt.Sprintf("test failed and panicked after %d retries.\n%v\n%v", executionIndex, panicExecution.panicData, panicExecution.panicStacktrace))
				}
			}
			wrapperFunc = &efdRetryFunc
		}
	}

	// Register the instrumented func as an internal instrumented func (to avoid double instrumentation)
	setInstrumentationMetadata(runtime.FuncForPC(reflect.Indirect(reflect.ValueOf(*wrapperFunc)).Pointer()), &instrumentationMetadata{IsInternal: true})
	return *wrapperFunc
}

//go:linkname testingTRunCleanup testing.(*common).runCleanup
func testingTRunCleanup(c *testing.T, ph int) (panicVal any)

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gotesting

import (
	"fmt"
	"reflect"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/constants"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils/net"
)

type (
	// instrumentationMetadata contains the internal instrumentation metadata
	instrumentationMetadata struct {
		IsInternal bool
	}

	// testExecutionMetadata contains metadata regarding an unique *testing.T or *testing.B execution
	testExecutionMetadata struct {
		test                        integrations.Test // internal CI Visibility test event
		error                       atomic.Int32      // flag to check if the test event has error data already
		skipped                     atomic.Int32      // flag to check if the test event has skipped data already
		panicData                   any               // panic data recovered from an internal test execution when using an additional feature wrapper
		panicStacktrace             string            // stacktrace from the panic recovered from an internal test
		isARetry                    bool              // flag to tag if a current test execution is a retry
		isANewTest                  bool              // flag to tag if a current test execution is part of a new test (EFD not known test)
		hasAdditionalFeatureWrapper bool              // flag to check if the current execution is part of an additional feature wrapper
	}

	// runTestWithRetryOptions contains the options for calling runTestWithRetry function
	runTestWithRetryOptions struct {
		targetFunc        func(t *testing.T)                                                            // target function to retry
		t                 *testing.T                                                                    // test to be executed
		initialRetryCount int64                                                                         // initial retry count
		adjustRetryCount  func(duration time.Duration) int64                                            // adjust retry count function depending on the duration of the first execution
		shouldRetry       func(ptrToLocalT *testing.T, executionIndex int, remainingRetries int64) bool // function to decide whether we want to perform a retry
		perExecution      func(ptrToLocalT *testing.T, executionIndex int, duration time.Duration)      // function to run after each test execution
		onRetryEnd        func(t *testing.T, executionIndex int, lastPtrToLocalT *testing.T)            // function executed when all execution have finished
		execMetaAdjust    func(execMeta *testExecutionMetadata, executionIndex int)                     // function to modify the execution metadata for each execution
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
	instrumentationMapMutex.Lock()
	defer instrumentationMapMutex.Unlock()
	instrumentationMap[fn] = metadata
}

// createTestMetadata creates the CI visibility test metadata associated with a given *testing.T, *testing.B, *testing.common
func createTestMetadata(tb testing.TB) *testExecutionMetadata {
	ciVisibilityTestMetadataMutex.Lock()
	defer ciVisibilityTestMetadataMutex.Unlock()
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
	ciVisibilityTestMetadataMutex.Lock()
	defer ciVisibilityTestMetadataMutex.Unlock()
	delete(ciVisibilityTestMetadata, reflect.ValueOf(tb).UnsafePointer())
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

	// Check if we have something to do, if not we bail out
	if !settings.FlakyTestRetriesEnabled && !settings.EarlyFlakeDetection.Enabled {
		return f
	}

	// Target function
	targetFunc := f

	// Early flake detection
	var earlyFlakeDetectionApplied bool
	if settings.EarlyFlakeDetection.Enabled {
		targetFunc, earlyFlakeDetectionApplied = applyEarlyFlakeDetectionAdditionalFeature(testInfo, targetFunc, settings)
	}

	// Flaky test retries (only if EFD was not applied and if the feature is enabled)
	if !earlyFlakeDetectionApplied && settings.FlakyTestRetriesEnabled {
		targetFunc, _ = applyFlakyTestRetriesAdditionalFeature(targetFunc)
	}

	// Register the instrumented func as an internal instrumented func (to avoid double instrumentation)
	setInstrumentationMetadata(runtime.FuncForPC(reflect.ValueOf(targetFunc).Pointer()), &instrumentationMetadata{IsInternal: true})
	return targetFunc
}

// applyFlakyTestRetriesAdditionalFeature applies the flaky test retries feature as a wrapper of a func(*testing.T)
func applyFlakyTestRetriesAdditionalFeature(targetFunc func(*testing.T)) (func(*testing.T), bool) {
	flakyRetrySettings := integrations.GetFlakyRetriesSettings()

	// If the retry count per test is > 1 and if we still have remaining total retry count
	if flakyRetrySettings.RetryCount > 1 && flakyRetrySettings.RemainingTotalRetryCount > 0 {
		return func(t *testing.T) {
			runTestWithRetry(&runTestWithRetryOptions{
				targetFunc:        targetFunc,
				t:                 t,
				initialRetryCount: flakyRetrySettings.RetryCount,
				adjustRetryCount:  nil, // No adjustRetryCount
				shouldRetry: func(ptrToLocalT *testing.T, executionIndex int, remainingRetries int64) bool {
					// Decide whether to retry
					return ptrToLocalT.Failed() && remainingRetries >= 0 && atomic.LoadInt64(&flakyRetrySettings.RemainingTotalRetryCount) >= 0
				},
				perExecution: func(ptrToLocalT *testing.T, executionIndex int, duration time.Duration) {
					if executionIndex > 0 {
						atomic.AddInt64(&flakyRetrySettings.RemainingTotalRetryCount, -1)
					}
				},
				onRetryEnd: func(t *testing.T, executionIndex int, lastPtrToLocalT *testing.T) {
					// Update original `t` with results from last execution
					tCommonPrivates := getTestPrivateFields(t)
					if tCommonPrivates == nil {
						panic("getting test private fields failed")
					}
					tCommonPrivates.SetFailed(lastPtrToLocalT.Failed())
					tCommonPrivates.SetSkipped(lastPtrToLocalT.Skipped())

					// Update parent status if failed
					if lastPtrToLocalT.Failed() {
						tParentCommonPrivates := getTestParentPrivateFields(t)
						if tParentCommonPrivates == nil {
							panic("getting test parent private fields failed")
						}
						tParentCommonPrivates.SetFailed(true)
					}

					// Print summary after retries
					if executionIndex > 0 {
						status := "passed"
						if t.Failed() {
							status = "failed"
						} else if t.Skipped() {
							status = "skipped"
						}

						fmt.Printf("    [ %v after %v retries by Datadog's auto test retries ]\n", status, executionIndex)

						// Check if total retry count was exceeded
						if atomic.LoadInt64(&flakyRetrySettings.RemainingTotalRetryCount) < 1 {
							fmt.Println("    the maximum number of total retries was exceeded.")
						}
					}
				},
				execMetaAdjust: nil, // No execMetaAdjust needed
			})
		}, true
	}
	return targetFunc, false
}

// applyEarlyFlakeDetectionAdditionalFeature applies the early flake detection feature as a wrapper of a func(*testing.T)
func applyEarlyFlakeDetectionAdditionalFeature(testInfo *commonInfo, targetFunc func(*testing.T), settings *net.SettingsResponseData) (func(*testing.T), bool) {
	earlyFlakeDetectionData := integrations.GetEarlyFlakeDetectionSettings()
	if earlyFlakeDetectionData != nil &&
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

		// If it's a new test, then we apply the EFD wrapper
		if !isAKnownTest {
			return func(t *testing.T) {
				var testPassCount, testSkipCount, testFailCount int

				runTestWithRetry(&runTestWithRetryOptions{
					targetFunc:        targetFunc,
					t:                 t,
					initialRetryCount: 0,
					adjustRetryCount: func(duration time.Duration) int64 {
						slowTestRetriesSettings := settings.EarlyFlakeDetection.SlowTestRetries
						durationSecs := duration.Seconds()
						if durationSecs < 5 {
							return int64(slowTestRetriesSettings.FiveS)
						} else if durationSecs < 10 {
							return int64(slowTestRetriesSettings.TenS)
						} else if durationSecs < 30 {
							return int64(slowTestRetriesSettings.ThirtyS)
						} else if duration.Minutes() < 5 {
							return int64(slowTestRetriesSettings.FiveM)
						}
						return 0
					},
					shouldRetry: func(ptrToLocalT *testing.T, executionIndex int, remainingRetries int64) bool {
						return remainingRetries >= 0
					},
					perExecution: func(ptrToLocalT *testing.T, executionIndex int, duration time.Duration) {
						// Collect test results
						if ptrToLocalT.Failed() {
							testFailCount++
						} else if ptrToLocalT.Skipped() {
							testSkipCount++
						} else {
							testPassCount++
						}
					},
					onRetryEnd: func(t *testing.T, executionIndex int, lastPtrToLocalT *testing.T) {
						// Update test status based on collected counts
						tCommonPrivates := getTestPrivateFields(t)
						if tCommonPrivates == nil {
							panic("getting test private fields failed")
						}
						status := "passed"
						if testPassCount == 0 {
							if testSkipCount > 0 {
								status = "skipped"
								tCommonPrivates.SetSkipped(true)
							}
							if testFailCount > 0 {
								status = "failed"
								tCommonPrivates.SetFailed(true)
								tParentCommonPrivates := getTestParentPrivateFields(t)
								if tParentCommonPrivates == nil {
									panic("getting test parent private fields failed")
								}
								tParentCommonPrivates.SetFailed(true)
							}
						}

						// Print summary after retries
						if executionIndex > 0 {
							fmt.Printf("  [ %v after %v retries by Datadog's early flake detection ]\n", status, executionIndex)
						}
					},
					execMetaAdjust: func(execMeta *testExecutionMetadata, executionIndex int) {
						// Set the flag new test to true
						execMeta.isANewTest = true
					},
				})
			}, true
		}
	}
	return targetFunc, false
}

// runTestWithRetry encapsulates the common retry logic for test functions.
func runTestWithRetry(options *runTestWithRetryOptions) {
	executionIndex := -1
	var panicExecution *testExecutionMetadata
	var lastPtrToLocalT *testing.T

	// Module and suite for this test
	var module integrations.TestModule
	var suite integrations.TestSuite

	// Check if we have execution metadata to propagate
	originalExecMeta := getTestMetadata(options.t)

	retryCount := options.initialRetryCount

	for {
		// Clear the matcher subnames map before each execution to avoid subname tests being called "parent/subname#NN" due to retries
		matcher := getTestContextMatcherPrivateFields(options.t)
		if matcher != nil {
			matcher.ClearSubNames()
		}

		// Increment execution index
		executionIndex++

		// Create a new local copy of `t` to isolate execution results
		ptrToLocalT := &testing.T{}
		copyTestWithoutParent(options.t, ptrToLocalT)

		// Create a dummy parent so we can run the test using this local copy
		// without affecting the test parent
		localTPrivateFields := getTestPrivateFields(ptrToLocalT)
		if localTPrivateFields == nil {
			panic("getting test private fields failed")
		}
		if localTPrivateFields.parent == nil {
			panic("parent of the test is nil")
		}
		*localTPrivateFields.parent = unsafe.Pointer(&testing.T{})

		// Create an execution metadata instance
		execMeta := createTestMetadata(ptrToLocalT)
		execMeta.hasAdditionalFeatureWrapper = true

		// Propagate set tags from a parent wrapper
		if originalExecMeta != nil {
			if originalExecMeta.isANewTest {
				execMeta.isANewTest = true
			}
			if originalExecMeta.isARetry {
				execMeta.isARetry = true
			}
		}

		// If we are in a retry execution, set the `isARetry` flag
		if executionIndex > 0 {
			execMeta.isARetry = true
		}

		// Adjust execution metadata
		if options.execMetaAdjust != nil {
			options.execMetaAdjust(execMeta, executionIndex)
		}

		// Run original func similar to how it gets run internally in tRunner
		startTime := time.Now()
		chn := make(chan struct{}, 1)
		go func() {
			defer func() {
				chn <- struct{}{}
			}()
			options.targetFunc(ptrToLocalT)
		}()
		<-chn
		duration := time.Since(startTime)

		// Call cleanup functions after this execution
		if err := testingTRunCleanup(ptrToLocalT, 1); err != nil {
			fmt.Printf("cleanup error: %v\n", err)
		}

		// Copy the current test to the wrapper if necessary
		if originalExecMeta != nil {
			originalExecMeta.test = execMeta.test
		}

		// Extract module and suite if present
		currentSuite := execMeta.test.Suite()
		if suite == nil && currentSuite != nil {
			suite = currentSuite
		}
		if module == nil && currentSuite != nil && currentSuite.Module() != nil {
			module = currentSuite.Module()
		}

		// Remove execution metadata
		deleteTestMetadata(ptrToLocalT)

		// Handle panic data
		if execMeta.panicData != nil {
			ptrToLocalT.Fail()
			if panicExecution == nil {
				panicExecution = execMeta
			}
		}

		// Adjust retry count after first execution if necessary
		if options.adjustRetryCount != nil && executionIndex == 0 {
			retryCount = options.adjustRetryCount(duration)
		}

		// Decrement retry count
		retryCount--

		// Call perExecution function
		if options.perExecution != nil {
			options.perExecution(ptrToLocalT, executionIndex, duration)
		}

		// Update lastPtrToLocalT
		lastPtrToLocalT = ptrToLocalT

		// Decide whether to continue
		if !options.shouldRetry(ptrToLocalT, executionIndex, retryCount) {
			break
		}
	}

	// Call onRetryEnd
	if options.onRetryEnd != nil {
		options.onRetryEnd(options.t, executionIndex, lastPtrToLocalT)
	}

	// After all test executions, check if we need to close the suite and the module
	if originalExecMeta == nil {
		checkModuleAndSuite(module, suite)
	}

	// Re-panic if test failed and panic data exists
	if options.t.Failed() && panicExecution != nil {
		// Ensure we flush all CI visibility data and close the session event
		integrations.ExitCiVisibility()
		panic(fmt.Sprintf("test failed and panicked after %d retries.\n%v\n%v", executionIndex, panicExecution.panicData, panicExecution.panicStacktrace))
	}
}

//go:linkname testingTRunCleanup testing.(*common).runCleanup
func testingTRunCleanup(c *testing.T, ph int) (panicVal any)

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gotesting

import (
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
)

type (
	// instrumentationMetadata contains the internal instrumentation metadata
	instrumentationMetadata struct {
		IsInternal bool
	}

	// testExecutionMetadata contains metadata regarding an unique *testing.T or *testing.B execution
	testExecutionMetadata struct {
		test                         integrations.Test // internal CI Visibility test event
		error                        atomic.Int32      // flag to check if the test event has error data already
		skipped                      atomic.Int32      // flag to check if the test event has skipped data already
		panicData                    any               // panic data recovered from an internal test execution when using an additional feature wrapper
		panicStacktrace              string            // stacktrace from the panic recovered from an internal test
		isARetry                     bool              // flag to tag if a current test execution is a retry
		isANewTest                   bool              // flag to tag if a current test a new test
		isAModifiedTest              bool              // flag to tag if a current test a modified test
		isEarlyFlakeDetectionEnabled bool              // flag to tag if Early Flake Detection is enabled for this execution
		isFlakyTestRetriesEnabled    bool              // flag to tag if Flaky Test Retries is enabled for this execution
		isQuarantined                bool              // flag to check if the test is quarantined
		isDisabled                   bool              // flag to check if the test is disabled
		isAttemptToFix               bool              // flag to check if the test is marked as attempt to fix
		isLastRetry                  bool              // flag to check if the current execution is the last retry
		allAttemptsPassed            bool              // flag to check if all attempts passed for a test marked as attempt to fix
		allRetriesFailed             bool              // flag to check if all retries failed for a test
		hasAdditionalFeatureWrapper  bool              // flag to check if the current execution is part of an additional feature wrapper
	}

	// runTestWithRetryOptions contains the options for calling runTestWithRetry function
	runTestWithRetryOptions struct {
		targetFunc func(t *testing.T) // target function to retry
		t          *testing.T         // test to be executed

		// function to modify the execution metadata before each execution (first callback executed). It's also called before postOnRetryEnd to do a final sync
		preExecMetaAdjust func(execMeta *testExecutionMetadata, executionIndex int)

		// function to decide whether we are in the last retry (second callback executed if we are in a retry execution)
		preIsLastRetry func(execMeta *testExecutionMetadata, executionIndex int, remainingRetries int64) bool

		// adjust retry count function depending on the duration of the first execution (first callback executed post test execution only in the first execution of the test)
		postAdjustRetryCount func(execMeta *testExecutionMetadata, duration time.Duration) int64

		// function to run after each test execution (second callback executed after test execution)
		postPerExecution func(ptrToLocalT *testing.T, execMeta *testExecutionMetadata, executionIndex int, duration time.Duration)

		// function to decide whether we want to perform a retry (third callback executed after test execution)
		postShouldRetry func(ptrToLocalT *testing.T, execMeta *testExecutionMetadata, executionIndex int, remainingRetries int64) bool

		// function executed when all execution have finished (last callback executed after all test executions(+retries))
		postOnRetryEnd func(t *testing.T, executionIndex int, lastPtrToLocalT *testing.T)
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
		}
		atomic.StoreInt32(&ciVisibilityEnabledValue, 0)
		return false

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

	// ensure that the additional features are initialized
	_ = integrations.GetKnownTests()

	// If none of the additional features are enabled, return the original function.
	if !settings.TestManagement.Enabled && !settings.EarlyFlakeDetection.Enabled && !settings.FlakyTestRetriesEnabled {
		return f
	}

	var meta struct {
		isTestManagementEnabled      bool
		isEarlyFlakeDetectionEnabled bool
		isFlakyTestRetriesEnabled    bool
		isQuarantined                bool
		isDisabled                   bool
		isAttemptToFix               bool
		isNew                        bool
		isModified                   bool
	}

	// init metadata
	meta.isTestManagementEnabled = settings.TestManagement.Enabled
	meta.isEarlyFlakeDetectionEnabled = settings.EarlyFlakeDetection.Enabled
	meta.isFlakyTestRetriesEnabled = settings.FlakyTestRetriesEnabled
	meta.isQuarantined = false
	meta.isDisabled = false
	meta.isAttemptToFix = false
	meta.isNew = false
	meta.isModified = false

	// Test Management feature
	if meta.isTestManagementEnabled {
		if data, ok := getTestManagementData(testInfo); ok && data != nil {
			meta.isQuarantined = data.Quarantined
			meta.isDisabled = data.Disabled
			meta.isAttemptToFix = data.AttemptToFix
		}
	}

	// Early Flake Detection feature
	if meta.isEarlyFlakeDetectionEnabled {
		isKnown, hasKnownData := isKnownTest(testInfo)
		meta.isNew = hasKnownData && !isKnown
	}

	// get the pointer to use the reference in the wrapper
	ptrMeta := &meta

	// function to detect if we should be in an efd execution
	isAnEfdExecution := func(execMeta *testExecutionMetadata) bool {
		isANewTest := execMeta.isANewTest
		isAModifiedTest := execMeta.isAModifiedTest && !execMeta.isAttemptToFix
		return execMeta.isEarlyFlakeDetectionEnabled && (isANewTest || isAModifiedTest)
	}

	// Create a unified wrapper that will use a single runTestWithRetry call.
	wrapper := func(t *testing.T) {
		t.Helper()
		originalExecMeta := getTestMetadata(t)

		// For Early Flake Detection: counters used to collect test results.
		var testPassCount, testSkipCount, testFailCount int
		// For Test Management and auto retries.
		var allAttemptsPassed int32 = 1
		var allRetriesFailed int32 = 1

		runTestWithRetry(&runTestWithRetryOptions{
			targetFunc: f,
			t:          t,
			preExecMetaAdjust: func(execMeta *testExecutionMetadata, executionIndex int) {
				// Synchronize the test execution metadata with the original test execution metadata.

				execMeta.isQuarantined = execMeta.isQuarantined || ptrMeta.isQuarantined
				execMeta.isDisabled = execMeta.isDisabled || ptrMeta.isDisabled
				execMeta.isAttemptToFix = execMeta.isAttemptToFix || ptrMeta.isAttemptToFix
				execMeta.isEarlyFlakeDetectionEnabled = execMeta.isEarlyFlakeDetectionEnabled || ptrMeta.isEarlyFlakeDetectionEnabled
				execMeta.isFlakyTestRetriesEnabled = execMeta.isFlakyTestRetriesEnabled || ptrMeta.isFlakyTestRetriesEnabled
				execMeta.allAttemptsPassed = atomic.LoadInt32(&allAttemptsPassed) == 1
				execMeta.allRetriesFailed = atomic.LoadInt32(&allRetriesFailed) == 1
				execMeta.isANewTest = execMeta.isANewTest || ptrMeta.isNew
				execMeta.isAModifiedTest = execMeta.isAModifiedTest || ptrMeta.isModified

				// Propagate flags from the original test metadata.
				propagateTestExecutionMetadataFlags(execMeta, originalExecMeta)

				ptrMeta.isQuarantined = execMeta.isQuarantined
				ptrMeta.isDisabled = execMeta.isDisabled
				ptrMeta.isAttemptToFix = execMeta.isAttemptToFix
				ptrMeta.isEarlyFlakeDetectionEnabled = execMeta.isEarlyFlakeDetectionEnabled
				ptrMeta.isFlakyTestRetriesEnabled = execMeta.isFlakyTestRetriesEnabled
				ptrMeta.isNew = execMeta.isANewTest
				ptrMeta.isModified = execMeta.isAModifiedTest
			},
			preIsLastRetry: func(execMeta *testExecutionMetadata, executionIndex int, remainingRetries int64) bool {
				if execMeta.isAttemptToFix || isAnEfdExecution(execMeta) {
					// For attempt-to-fix tests and EFD, the last retry is when remaining retries == 1.
					return remainingRetries == 1
				}

				// FlakyTestRetries also considers the global remaining retry count.
				if execMeta.isFlakyTestRetriesEnabled {
					return remainingRetries == 1 || atomic.LoadInt64(&integrations.GetFlakyRetriesSettings().RemainingTotalRetryCount) == 1
				}

				return false
			},
			postAdjustRetryCount: func(execMeta *testExecutionMetadata, duration time.Duration) int64 {
				// adjust retry count only runs after the first run

				// Attempt To Fix retries are always set to the configured value.
				if execMeta.isAttemptToFix {
					return int64(settings.TestManagement.AttemptToFixRetries)
				}

				// Early Flake Detection adjusts the retry count based on test duration.
				if isAnEfdExecution(execMeta) {
					slowTestRetries := settings.EarlyFlakeDetection.SlowTestRetries
					secs := duration.Seconds()
					if secs < 5 {
						return int64(slowTestRetries.FiveS)
					} else if secs < 10 {
						return int64(slowTestRetries.TenS)
					} else if secs < 30 {
						return int64(slowTestRetries.ThirtyS)
					} else if duration.Minutes() < 5 {
						return int64(slowTestRetries.FiveM)
					}
				}

				// Automatic flaky tests retries are set to the configured value.
				if execMeta.isFlakyTestRetriesEnabled {
					return integrations.GetFlakyRetriesSettings().RetryCount
				}

				// No retries
				return 0
			},
			postPerExecution: func(ptrToLocalT *testing.T, execMeta *testExecutionMetadata, executionIndex int, duration time.Duration) {
				if ptrToLocalT.Failed() || ptrToLocalT.Skipped() {
					atomic.StoreInt32(&allAttemptsPassed, 0)
				}
				if !ptrToLocalT.Failed() {
					atomic.StoreInt32(&allRetriesFailed, 0)
				}

				if execMeta.isAttemptToFix {
					status := "PASS"
					if ptrToLocalT.Failed() {
						status = "FAIL"
					} else if ptrToLocalT.Skipped() {
						status = "SKIP"
					}

					ptrToLocalT.Logf("  [attempt to fix retry: %d (%s)]", executionIndex+1, status)
					return
				}

				if isAnEfdExecution(execMeta) {
					if ptrToLocalT.Failed() {
						testFailCount++
					} else if ptrToLocalT.Skipped() {
						testSkipCount++
					} else {
						testPassCount++
					}
					return
				}

				if execMeta.isFlakyTestRetriesEnabled {
					if executionIndex > 0 {
						atomic.AddInt64(&integrations.GetFlakyRetriesSettings().RemainingTotalRetryCount, -1)
					}
					return
				}
			},
			postShouldRetry: func(ptrToLocalT *testing.T, execMeta *testExecutionMetadata, executionIndex int, remainingRetries int64) bool {
				if execMeta.isAttemptToFix {
					// For attempt-to-fix tests, retry if remaining retries > 0.
					return remainingRetries > 0
				}

				if isAnEfdExecution(execMeta) {
					// For EFD, retry if remaining retries >= 0.
					return remainingRetries >= 0
				}

				if execMeta.isFlakyTestRetriesEnabled {
					// For flaky test retries, retry if the test failed and remaining retries >= 0.
					return ptrToLocalT.Failed() && remainingRetries >= 0 &&
						atomic.LoadInt64(&integrations.GetFlakyRetriesSettings().RemainingTotalRetryCount) >= 0
				}

				// No retries for other cases.
				return false
			},
			postOnRetryEnd: func(t *testing.T, executionIndex int, lastPtrToLocalT *testing.T) {
				// if the test is disabled or quarantined, skip the test result to the testing framework
				if ptrMeta.isDisabled || ptrMeta.isQuarantined {
					t.SkipNow()
					return
				}

				// get the test common privates
				tCommonPrivates := getTestPrivateFields(t)
				if tCommonPrivates == nil {
					panic("getting test private fields failed")
				}

				// if early flake detection is enabled, we need to set the test status
				efdOnNewTest := ptrMeta.isEarlyFlakeDetectionEnabled && ptrMeta.isNew
				efdOnModifiedTest := ptrMeta.isEarlyFlakeDetectionEnabled && ptrMeta.isModified && !ptrMeta.isAttemptToFix
				if efdOnNewTest || efdOnModifiedTest {
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
					if executionIndex > 0 {
						fmt.Printf("  [ %v after %v retries by Datadog's early flake detection ]\n", status, executionIndex)
					}
					return
				}

				// if the test is a flaky test retries test, we need to set the test status
				if ptrMeta.isFlakyTestRetriesEnabled {
					tCommonPrivates.SetFailed(lastPtrToLocalT.Failed())
					tCommonPrivates.SetSkipped(lastPtrToLocalT.Skipped())
					if lastPtrToLocalT.Failed() {
						tParentCommonPrivates := getTestParentPrivateFields(t)
						if tParentCommonPrivates == nil {
							panic("getting test parent private fields failed")
						}
						tParentCommonPrivates.SetFailed(true)
					}
					if executionIndex > 0 {
						status := "passed"
						if t.Failed() {
							status = "failed"
						} else if t.Skipped() {
							status = "skipped"
						}
						fmt.Printf("    [ %v after %v retries by Datadog's auto test retries ]\n", status, executionIndex)
						if atomic.LoadInt64(&integrations.GetFlakyRetriesSettings().RemainingTotalRetryCount) < 1 {
							fmt.Println("    the maximum number of total retries was exceeded.")
						}
					}
					return
				}
			},
		})
	}

	// Mark the wrapper as instrumented.
	setInstrumentationMetadata(runtime.FuncForPC(reflect.ValueOf(wrapper).Pointer()), &instrumentationMetadata{IsInternal: true})
	return wrapper
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

	retryCount := int64(0)
	var lastExecMeta *testExecutionMetadata

	// Set this func as a helper func of t
	options.t.Helper()
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
		ptrToLocalT.Helper()

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
		propagateTestExecutionMetadataFlags(execMeta, originalExecMeta)

		// If we are in a retry execution, set the `isARetry` flag
		execMeta.isARetry = executionIndex > 0

		// Adjust execution metadata
		if options.preExecMetaAdjust != nil {
			options.preExecMetaAdjust(execMeta, executionIndex)
		}

		// Set if we are in the last retry
		if execMeta.isARetry {
			execMeta.isLastRetry = options.preIsLastRetry(execMeta, executionIndex, retryCount)
		}

		// Run original func similar to how it gets run internally in tRunner
		startTime := time.Now()
		chn := make(chan struct{}, 1)
		go func() {
			defer func() {
				chn <- struct{}{}
			}()
			ptrToLocalT.Helper()
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
		if options.postAdjustRetryCount != nil && executionIndex == 0 {
			retryCount = options.postAdjustRetryCount(execMeta, duration)
		}

		// Decrement retry count
		retryCount--

		// Call perExecution function
		if options.postPerExecution != nil {
			options.postPerExecution(ptrToLocalT, execMeta, executionIndex, duration)
		}

		// Update lastPtrToLocalT
		lastPtrToLocalT = ptrToLocalT
		lastExecMeta = execMeta

		// Decide whether to continue
		if !options.postShouldRetry(ptrToLocalT, execMeta, executionIndex, retryCount) {
			break
		}
	}

	// Adjust execution metadata
	if options.preExecMetaAdjust != nil {
		options.preExecMetaAdjust(lastExecMeta, executionIndex)
	}

	// Call onRetryEnd
	if options.postOnRetryEnd != nil {
		options.postOnRetryEnd(options.t, executionIndex, lastPtrToLocalT)
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

// propagateTestExecutionMetadataFlags propagates the test execution metadata flags from the original test execution metadata to the current one.
func propagateTestExecutionMetadataFlags(execMeta *testExecutionMetadata, originalExecMeta *testExecutionMetadata) {
	if execMeta == nil || originalExecMeta == nil {
		return
	}

	// Propagate the test execution metadata
	execMeta.isANewTest = execMeta.isANewTest || originalExecMeta.isANewTest
	execMeta.isAModifiedTest = execMeta.isAModifiedTest || originalExecMeta.isAModifiedTest
	execMeta.isARetry = execMeta.isARetry || originalExecMeta.isARetry
	execMeta.isEarlyFlakeDetectionEnabled = execMeta.isEarlyFlakeDetectionEnabled || originalExecMeta.isEarlyFlakeDetectionEnabled
	execMeta.isFlakyTestRetriesEnabled = execMeta.isFlakyTestRetriesEnabled || originalExecMeta.isFlakyTestRetriesEnabled
	execMeta.isQuarantined = execMeta.isQuarantined || originalExecMeta.isQuarantined
	execMeta.isDisabled = execMeta.isDisabled || originalExecMeta.isDisabled
	execMeta.isAttemptToFix = execMeta.isAttemptToFix || originalExecMeta.isAttemptToFix
}

//go:linkname testingTRunCleanup testing.(*common).runCleanup
func testingTRunCleanup(c *testing.T, ph int) (panicVal any)

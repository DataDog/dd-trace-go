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
	"unsafe"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/constants"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations"
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
	ciVisibilityTestMetadataMutex.RLock()
	defer ciVisibilityTestMetadataMutex.RUnlock()
	ptr := reflect.ValueOf(tb).UnsafePointer()
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

// checkIfCIVisibilityExitIsRequiredByPanic checks the additional features settings to decide if we allow individual tests to panic or not
func checkIfCIVisibilityExitIsRequiredByPanic() bool {
	// Apply additional features
	settings := integrations.GetSettings()

	// If we don't plan to do retries then we allow to panic
	return !settings.FlakyTestRetriesEnabled && !settings.EarlyFlakeDetection.Enabled
}

// applyAdditionalFeaturesToTestFunc applies all the additional features as wrapper of a func(*testing.T)
func applyAdditionalFeaturesToTestFunc(f func(*testing.T)) func(*testing.T) {
	// Apply additional features
	settings := integrations.GetSettings()

	// Wrapper function
	wrapperFunc := f

	// Flaky test retries
	if settings.FlakyTestRetriesEnabled {
		flakyRetrySettings := integrations.GetFlakyRetriesSettings()

		// if the retry count per test is > 1 and if we still have remaining total retry count
		if flakyRetrySettings.RetryCount > 1 && flakyRetrySettings.RemainingTotalRetryCount > 0 {
			wrapperFunc = func(t *testing.T) {
				retryCount := flakyRetrySettings.RetryCount
				executionIndex := -1
				var panicExecution *testExecutionMetadata

				// Get the private fields from the *testing.T instance
				tParentCommonPrivates := getTestParentPrivateFields(t)

				// Module and suite for this test
				var module integrations.DdTestModule
				var suite integrations.DdTestSuite

				for {
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
						f(ptrToLocalT)
					}()
					<-chn

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

					fmt.Printf("    [ %v after %v retries ]\n", status, retries)
				}

				// after all test executions we check if we need to close the suite and the module
				checkModuleAndSuite(module, suite)

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
		}
	}

	// Register the instrumented func as an internal instrumented func (to avoid double instrumentation)
	setInstrumentationMetadata(runtime.FuncForPC(reflect.Indirect(reflect.ValueOf(wrapperFunc)).Pointer()), &instrumentationMetadata{IsInternal: true})
	return wrapperFunc
}

//go:linkname testingTRunCleanup testing.(*common).runCleanup
func testingTRunCleanup(c *testing.T, ph int) (panicVal any)

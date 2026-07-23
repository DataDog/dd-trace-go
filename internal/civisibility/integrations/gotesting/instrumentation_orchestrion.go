// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gotesting

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
	_ "unsafe" // required blank import to run orchestrion

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// instrumentCaptureFormattedError records the value already formatted by the
// native testing method and returns it unchanged for testing.common.log.
//
//go:linkname instrumentCaptureFormattedError
func instrumentCaptureFormattedError(tb testing.TB, errType, message string, skip int) string {
	formatted := message
	release, ok := acquireOrchestrionTestingHook()
	if !ok {
		return formatted
	}
	defer release()
	if errType == "Error" || errType == "Fatal" {
		message = strings.TrimSuffix(message, "\n")
	}
	if isProcessRetryChild() {
		recordProcessRetryChildErrorInfo(tb, errType, message, 2+skip)
		return formatted
	}
	if !isCiVisibilityEnabled() {
		return formatted
	}
	if execMeta := getTestMetadata(tb); execMeta != nil {
		execMeta.processRetryError.CompareAndSwap(nil, &processRetryErrorInfo{
			Type:    errType,
			Message: message,
			Stack:   utils.GetStacktrace(2 + skip),
		})
	}
	return formatted
}

// instrumentCaptureFormattedSkip records the value already formatted by the
// native testing method and returns it unchanged for testing.common.log.
//
//go:linkname instrumentCaptureFormattedSkip
func instrumentCaptureFormattedSkip(tb testing.TB, skipType, reason string) string {
	formatted := reason
	release, ok := acquireOrchestrionTestingHook()
	if !ok {
		return formatted
	}
	defer release()
	if skipType == "Skip" {
		reason = strings.TrimSuffix(reason, "\n")
	}
	if isProcessRetryChild() {
		if execMeta := getTestMetadata(tb); execMeta != nil && processRetryChildOwnerMetadata(execMeta) == execMeta {
			execMeta.processRetrySkipReason.CompareAndSwap(nil, &reason)
		}
		return formatted
	}
	if !isCiVisibilityEnabled() {
		return formatted
	}
	if execMeta := getTestMetadata(tb); execMeta != nil {
		execMeta.processRetrySkipReason.CompareAndSwap(nil, &reason)
	}
	return formatted
}

// ******************************************************************************************************************
// WARNING: DO NOT CHANGE THE SIGNATURE OF THESE FUNCTIONS!
//
//  The following functions are being used by both the manual api and most importantly the Orchestrion automatic
//  instrumentation integration.
// ******************************************************************************************************************

// instrumentTestingM preserves the finalizer-only ABI used by published v1
// Orchestrion advice.
//
//go:linkname instrumentTestingM
func instrumentTestingM(m *testing.M) func(exitCode int) {
	_, finalize := instrumentTestingMWithOptions(m, processRetryWrapperOptions())
	return func(exitCode int) {
		_ = finalize(exitCode)
	}
}

// instrumentTestingMWithControl instruments testing.M and tells current
// Orchestrion advice whether the native M.Run body should execute.
//
//go:linkname instrumentTestingMWithControl
func instrumentTestingMWithControl(m *testing.M) (bool, func(int) int) {
	proceed, finalize := instrumentTestingMWithOptions(m, processRetryWrapperOptions())
	return proceed, finalize
}

// instrumentTestingBuiltWithOrchestrion records that testing.M.Run has woven ownership.
//
//go:linkname instrumentTestingBuiltWithOrchestrion
func instrumentTestingBuiltWithOrchestrion() {
	markTestingBuiltWithOrchestrion()
}

// instrumentTestingTFunc helper function to instrument a testing function func(*testing.T)
//
//go:linkname instrumentTestingTFunc
func instrumentTestingTFunc(f func(*testing.T)) func(*testing.T) {
	release, ok := acquireOrchestrionTestingHook()
	if !ok {
		return f
	}
	defer release()
	if isProcessRetryChild() {
		return instrumentProcessRetryChildSubtest(f)
	}

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

		subtestIdentity := newTestIdentity(moduleName, suiteName, t.Name())
		isSubtest := len(subtestIdentity.Segments) > 1

		var testPrivateFields *commonPrivateFields
		var parentExecMeta *testExecutionMetadata

		if isSubtest {
			testPrivateFields = getTestPrivateFields(t)
			if testPrivateFields != nil && testPrivateFields.parent != nil {
				parentExecMeta = getTestMetadataFromPointer(*testPrivateFields.parent)
			}

			settings := integrations.GetSettings()
			shouldInstrument := settings != nil && settings.SubtestFeaturesEnabled
			hasDirective := false

			log.Debug("subtest gating module=%s suite=%s identity=%s", moduleName, suiteName, subtestIdentity.FullName)

			if parentExecMeta != nil {
				if parentExecMeta.isAttemptToFix || parentExecMeta.isDisabled || parentExecMeta.isQuarantined {
					hasDirective = true
					log.Debug("subtest gating parent directive for %s: attempt_to_fix=%t disabled=%t quarantined=%t",
						subtestIdentity.FullName, parentExecMeta.isAttemptToFix, parentExecMeta.isDisabled, parentExecMeta.isQuarantined)
				}
			}

			if !hasDirective && shouldInstrument {
				if data, matchKind, hasData := getTestManagementData(subtestIdentity); hasData && matchKind == testManagementMatchExact && data != nil {
					if data.Disabled || data.Quarantined || data.AttemptToFix {
						hasDirective = true
						log.Debug("subtest gating exact match for %s: disabled=%t quarantined=%t attempt_to_fix=%t",
							subtestIdentity.FullName, data.Disabled, data.Quarantined, data.AttemptToFix)
					}
				} else {
					log.Debug("subtest gating no exact match for %s (hasData=%t matchKind=%d)", subtestIdentity.FullName, hasData, matchKind)
				}
			}
		}

		subtestInfo := &commonInfo{
			moduleName: moduleName,
			suiteName:  suiteName,
			testName:   subtestIdentity.FullName,
			identity:   subtestIdentity,
		}

		runSubtest := func(currentT *testing.T) {
			localIdentity := subtestIdentity
			if currentT.Name() != subtestIdentity.FullName {
				// Nested subtests have their own full identity path.
				localIdentity = newTestIdentity(moduleName, suiteName, currentT.Name())
			}

			addModulesCounters(moduleName, 1)
			addSuitesCounters(suiteName, 1)

			log.Debug("instrumentTestingTFunc: creating test span for %s", currentT.Name())

			module := session.GetOrCreateModule(moduleName)
			suite := module.GetOrCreateSuite(suiteName)
			test := suite.CreateTest(currentT.Name())
			startTime := time.Now()

			if testifyData != nil {
				// Testify-based suites expose the original method so we should record that.
				test.SetTestFunc(testifyData.methodFunc)
			} else {
				// Otherwise fall back to the standard testing function pointer.
				test.SetTestFunc(originalFunc)
			}

			execMeta := getTestMetadata(currentT)
			if execMeta == nil {
				// Create fresh metadata when additional-feature wrappers were not executed above us.
				execMeta = createTestMetadata(currentT, nil)
				defer deleteTestMetadata(currentT)
			}
			execMeta.identity = localIdentity

			currentPrivates := getTestPrivateFields(currentT)
			if currentPrivates != nil && currentPrivates.parent != nil {
				parentFromCurrent := getTestMetadataFromPointer(*currentPrivates.parent)
				propagateTestExecutionMetadataFlags(execMeta, parentFromCurrent)
				execMeta.isFreshRetryAttemptDescendant = parentFromCurrent != nil &&
					(parentFromCurrent.usesFreshRetryAttemptRuntime || parentFromCurrent.isFreshRetryAttemptDescendant)
			}

			cancelExecution := setTestTagsFromExecutionMetadata(test, execMeta)
			if cancelExecution {
				if !execMeta.hasAdditionalFeatureWrapper {
					// Disabled fast-path subtests close their test event before normal finalization is registered.
					checkModuleAndSuite(module, suite)
				}
				return
			}

			bodyReturned := false
			defer func() {
				r := recover()
				bodyDuration := time.Since(startTime)

				if execMeta.usesFreshRetryAttemptRuntime {
					bodyTerminal := r
					bodyStack := ""
					if bodyTerminal != nil {
						bodyStack = utils.GetStacktrace(1)
					}
					execMeta.retryAttemptFinalizer = func(result retryAttemptResult) {
						terminal := bodyTerminal
						terminalStack := bodyStack
						if result.panicData != nil {
							terminal = result.panicData
							terminalStack = string(result.panicStack)
						}
						if result.cleanupPanicData != nil {
							terminal = result.cleanupPanicData
							terminalStack = string(result.cleanupPanicStack)
						}
						logFreshRetryAttemptState("finalize_orchestrion", currentT, result)
						finalizeInstrumentedTestExecution(currentT, execMeta, test, suite, module, result.duration, result.output, terminal, terminalStack, false)
					}
					if r != nil {
						panic(r)
					}
					return
				}

				unexpectedTermination := r == nil && processRetryUnexpectedTestTermination(currentT, bodyReturned)
				duration := runAndApplyTestCleanupWithDuration(currentT, execMeta, bodyDuration)
				if unexpectedTermination {
					r = unexpectedTestTerminationMessage
				}
				terminalStack := ""
				if r != nil {
					terminalStack = utils.GetStacktrace(1)
				}
				finalizeInstrumentedTestExecution(currentT, execMeta, test, suite, module, duration, nil, r, terminalStack, false)
				nativeTerminal := r
				if nativeTerminal == nil && execMeta.cleanupResult != nil {
					nativeTerminal = execMeta.cleanupResult.panicData
				}
				if nativeTerminal != nil && !execMeta.hasAdditionalFeatureWrapper {
					checkModuleAndSuite(module, suite)
					integrations.ExitCiVisibility()
					panic(nativeTerminal)
				}
				if !execMeta.hasAdditionalFeatureWrapper {
					// Additional-feature wrappers own module and suite closure after all retry attempts finish.
					checkModuleAndSuite(module, suite)
				}
			}()

			if !execMeta.suppressUserTestBody {
				f(currentT)
			}
			bodyReturned = true
		}

		wrappedFunc := applyAdditionalFeaturesToTestFunc(runSubtest, subtestInfo, parentExecMeta, additionalFeatureWrapperOptions{})
		wrappedFunc(t)
	}

	setInstrumentationMetadata(runtime.FuncForPC(reflect.Indirect(reflect.ValueOf(instrumentedFn)).Pointer()), &instrumentationMetadata{IsInternal: true})
	return instrumentedFn
}

// instrumentSetErrorInfo helper function to set an error in the `*testing.T, *testing.B, *testing.common` CI Visibility span
//
//go:linkname instrumentSetErrorInfo
func instrumentSetErrorInfo(tb testing.TB, errType string, errMessage string, skip int) {
	release, ok := acquireOrchestrionTestingHook()
	if !ok {
		return
	}
	defer release()
	if isProcessRetryChild() {
		recordProcessRetryChildErrorInfo(tb, errType, errMessage, 2+skip)
		markProcessRetryChildFailed(tb)
		return
	}

	// Check if CI Visibility was disabled using the kill switch before
	if !isCiVisibilityEnabled() {
		return
	}

	// Get the CI Visibility span and check if we can set the error type, message and stack
	ciTestItem := getTestMetadata(tb)
	if ciTestItem != nil && ciTestItem.test != nil && ciTestItem.error.CompareAndSwap(0, 1) {
		stack := utils.GetStacktrace(2 + skip)
		if formatted := ciTestItem.processRetryError.Load(); formatted != nil {
			errType = formatted.Type
			errMessage = formatted.Message
			stack = formatted.Stack
		}
		log.Debug("instrumentSetErrorInfo: setting error info [name: %q, type: %q, message: %q]", ciTestItem.test.Name(), errType, errMessage)
		ciTestItem.test.SetError(integrations.WithErrorInfo(errType, errMessage, stack))

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
	release, ok := acquireOrchestrionTestingHook()
	if !ok {
		return
	}
	defer release()
	if isProcessRetryChild() {
		if execMeta := getTestMetadata(tb); execMeta != nil && processRetryChildOwnerMetadata(execMeta) == execMeta {
			reason := truncateProcessRetrySkipReason(skipReason)
			execMeta.processRetrySkipReason.CompareAndSwap(nil, &reason)
		}
		return
	}

	// Check if CI Visibility was disabled using the kill switch before
	if !isCiVisibilityEnabled() {
		return
	}

	// Get the CI Visibility span and check if we can mark it as skipped and close it
	ciTestItem := getTestMetadata(tb)
	if ciTestItem != nil && ciTestItem.test != nil && ciTestItem.skipped.CompareAndSwap(0, 1) {
		log.Debug("instrumentCloseAndSkip: skipping test [name: %q, reason: %q]", ciTestItem.test.Name(), skipReason)
		// If there's an additional feature wrapper (retry/EFD), let the defer block handle closing
		// so that test.final_status can be set properly. Store the skip reason for the defer block.
		if ciTestItem.hasAdditionalFeatureWrapper {
			ciTestItem.skipReason = skipReason
			return
		}
		// For single-execution tests (no wrapper), this is the final execution.
		// Set test.final_status before closing.
		ciTestItem.test.SetTag(constants.TestFinalStatus, constants.TestStatusSkip)
		ciTestItem.test.Close(integrations.ResultStatusSkip, integrations.WithTestSkipReason(skipReason))
	}
}

// instrumentSkipNow helper function to close and skip a `*testing.T, *testing.B, *testing.common` CI Visibility span
//
//go:linkname instrumentSkipNow
func instrumentSkipNow(tb testing.TB) {
	release, ok := acquireOrchestrionTestingHook()
	if !ok {
		return
	}
	defer release()
	if isProcessRetryChild() {
		return
	}

	// Check if CI Visibility was disabled using the kill switch before
	if !isCiVisibilityEnabled() {
		return
	}

	// Get the CI Visibility span and check if we can mark it as skipped and close it
	ciTestItem := getTestMetadata(tb)
	if ciTestItem != nil && ciTestItem.test != nil && ciTestItem.skipped.CompareAndSwap(0, 1) {
		if formatted := ciTestItem.processRetrySkipReason.Load(); formatted != nil {
			ciTestItem.skipReason = *formatted
		}
		log.Debug("instrumentSkipNow: skipping test [name: %q, reason: %q]", ciTestItem.test.Name(), ciTestItem.skipReason)
		// If there's an additional feature wrapper (retry/EFD), let the defer block handle closing
		// so that test.final_status can be set properly.
		if ciTestItem.hasAdditionalFeatureWrapper {
			return
		}
		// For single-execution tests (no wrapper), this is the final execution.
		// Set test.final_status before closing.
		ciTestItem.test.SetTag(constants.TestFinalStatus, constants.TestStatusSkip)
		if ciTestItem.skipReason != "" {
			ciTestItem.test.Close(integrations.ResultStatusSkip, integrations.WithTestSkipReason(ciTestItem.skipReason))
		} else {
			ciTestItem.test.Close(integrations.ResultStatusSkip)
		}
	}
}

// instrumentTestingBFunc helper function to instrument a benchmark function func(*testing.B)
//
//go:linkname instrumentTestingBFunc
func instrumentTestingBFunc(pb *testing.B, name string, f func(*testing.B)) (string, func(*testing.B)) {
	release, ok := acquireOrchestrionTestingHook()
	if !ok {
		return name, f
	}
	defer release()
	if isProcessRetryChild() {
		return name, f
	}

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
	release, ok := acquireOrchestrionTestingHook()
	if !ok {
		return
	}
	defer release()
	if isProcessRetryChild() {
		return
	}

	log.Debug("instrumentTestifySuiteRun: instrumenting testify suite run")
	registerTestifySuite(t, suite)
}

// getTestOptimizationContext helper function to get the context of the test
//
//go:linkname getTestOptimizationContext
func getTestOptimizationContext(tb testing.TB) context.Context {
	release, ok := acquireOrchestrionTestingHook()
	if !ok {
		return context.Background()
	}
	defer release()
	if isProcessRetryChild() {
		return context.Background()
	}

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
	release, ok := acquireOrchestrionTestingHook()
	if !ok {
		return nil
	}
	defer release()
	ciTestItem := getTestMetadata(tb)
	if ciTestItem != nil && ciTestItem.test != nil {
		log.Debug("getTestOptimizationTest: returning test from metadata")
		return ciTestItem.test
	}

	return nil
}

// instrumentTestingParallel reports whether CI Visibility has replaced the
// native Parallel implementation. Retry attempts now use a fresh testing.T and
// bridge the real scheduler directly, so Parallel always remains native here.
//
//go:linkname instrumentTestingParallel
func instrumentTestingParallel(t *testing.T) bool {
	_ = t
	return false
}

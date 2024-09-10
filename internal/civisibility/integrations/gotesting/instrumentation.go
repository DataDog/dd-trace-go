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
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils"
)

// The following functions are being used by the gotesting package for manual instrumentation and the orchestrion
// automatic instrumentation

type (
	instrumentationMetadata struct {
		IsInternal       bool
		OriginalTest     *func(*testing.T)
		InstrumentedTest *func(*testing.T)
	}

	ddTestItem struct {
		test    integrations.DdTest
		error   bool
		skipped bool
	}
)

var (
	// instrumentationMap holds a map of *runtime.Func for tracking instrumented functions
	instrumentationMap = map[*runtime.Func]*instrumentationMetadata{}

	// instrumentationMapMutex is a read-write mutex for synchronizing access to instrumentationMap.
	instrumentationMapMutex sync.RWMutex

	// ciVisibilityTests holds a map of *testing.T or *testing.B to civisibility.DdTest for tracking tests.
	ciVisibilityTests = map[unsafe.Pointer]*ddTestItem{}

	// ciVisibilityTestsMutex is a read-write mutex for synchronizing access to ciVisibilityTests.
	ciVisibilityTestsMutex sync.RWMutex
)

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

// getCiVisibilityTest retrieves the CI visibility test associated with a given *testing.T or *testing.B
func getCiVisibilityTest(tb testing.TB) *ddTestItem {
	ciVisibilityTestsMutex.RLock()
	defer ciVisibilityTestsMutex.RUnlock()

	if v, ok := ciVisibilityTests[reflect.ValueOf(tb).UnsafePointer()]; ok {
		return v
	}

	return nil
}

// setCiVisibilityTest associates a CI visibility test with a given *testing.T or *testing.B
func setCiVisibilityTest(tb testing.TB, ciTest integrations.DdTest) {
	ciVisibilityTestsMutex.Lock()
	defer ciVisibilityTestsMutex.Unlock()
	ciVisibilityTests[reflect.ValueOf(tb).UnsafePointer()] = &ddTestItem{test: ciTest}
}

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

	// Avoid instrumenting twice
	metadata := getInstrumentationMetadata(originalFunc)
	if metadata != nil {
		// If is an internal test, we don't instrument because f is already the instrumented func by executeInternalTest
		if metadata.IsInternal {
			return f
		}

		return *metadata.InstrumentedTest
	}

	instrumentedFunc := func(t *testing.T) {
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

	metadata = &instrumentationMetadata{
		IsInternal:       false,
		OriginalTest:     &f,
		InstrumentedTest: &instrumentedFunc,
	}

	setInstrumentationMetadata(originalFunc, metadata)
	setInstrumentationMetadata(runtime.FuncForPC(reflect.Indirect(reflect.ValueOf(instrumentedFunc)).Pointer()), metadata)
	return instrumentedFunc
}

// instrumentSetErrorInfo helper function to set an error in the `testing.T or testing.B` CI Visibility span
func instrumentSetErrorInfo(tb testing.TB, errType string, errMessage string, skip int) {
	ciTestItem := getCiVisibilityTest(tb)
	if ciTestItem != nil && !ciTestItem.error && ciTestItem.test != nil {
		ciTestItem.error = true
		ciTestItem.test.SetErrorInfo(errType, errMessage, utils.GetStacktrace(2+skip))
	}
}

// instrumentCloseAndSkip helper function to close and skip with a reason a `testing.T or testing.B` CI Visibility span
func instrumentCloseAndSkip(tb testing.TB, skipReason string) {
	ciTestItem := getCiVisibilityTest(tb)
	if ciTestItem != nil && !ciTestItem.skipped && ciTestItem.test != nil {
		ciTestItem.skipped = true
		ciTestItem.test.CloseWithFinishTimeAndSkipReason(integrations.ResultStatusSkip, time.Now(), skipReason)
	}
}

// instrumentSkipNow helper function to close and skip a `testing.T or testing.B` CI Visibility span
func instrumentSkipNow(tb testing.TB) {
	ciTestItem := getCiVisibilityTest(tb)
	if ciTestItem != nil && !ciTestItem.skipped && ciTestItem.test != nil {
		ciTestItem.skipped = true
		ciTestItem.test.Close(integrations.ResultStatusSkip)
	}
}

// instrumentTestingBFunc helper function to instrument a benchmark function func(*testing.B)
func instrumentTestingBFunc(pb *testing.B, name string, f func(*testing.B)) (string, func(*testing.B)) {
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
			// Set b to the CI visibility test.
			setCiVisibilityTest(b, test)

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

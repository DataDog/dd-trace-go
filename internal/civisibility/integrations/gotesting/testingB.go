// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gotesting

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils"
)

var (
	// ciVisibilityBenchmarks holds a map of *testing.B to civisibility.DdTest for tracking benchmarks.
	ciVisibilityBenchmarks = map[*testing.B]integrations.DdTest{}

	// ciVisibilityBenchmarksMutex is a read-write mutex for synchronizing access to ciVisibilityBenchmarks.
	ciVisibilityBenchmarksMutex sync.RWMutex

	// subBenchmarkAutoName is a placeholder name for CI Visibility sub-benchmarks.
	subBenchmarkAutoName = "[DD:TestVisibility]"

	// subBenchmarkAutoNameRegex is a regex pattern to match the sub-benchmark auto name.
	subBenchmarkAutoNameRegex = regexp.MustCompile(`(?si)\/\[DD:TestVisibility\].*`)
)

// B is a type alias for testing.B to provide additional methods for CI visibility.
type B testing.B

// GetBenchmark is a helper to return *gotesting.B from *testing.B.
// Internally, it is just a (*gotesting.B)(b) cast.
func GetBenchmark(t *testing.B) *B { return (*B)(t) }

// Run benchmarks f as a subbenchmark with the given name. It reports
// whether there were any failures.
//
// A subbenchmark is like any other benchmark. A benchmark that calls Run at
// least once will not be measured itself and will be called once with N=1.
func (ddb *B) Run(name string, f func(*testing.B)) bool {
	// Reflect the function to obtain its pointer.
	fReflect := reflect.Indirect(reflect.ValueOf(f))
	moduleName, suiteName := utils.GetModuleAndSuiteName(fReflect.Pointer())
	originalFunc := runtime.FuncForPC(fReflect.Pointer())

	// Increment the test count in the module.
	atomic.AddInt32(modulesCounters[moduleName], 1)

	// Increment the test count in the suite.
	atomic.AddInt32(suitesCounters[suiteName], 1)

	pb := (*testing.B)(ddb)
	return pb.Run(subBenchmarkAutoName, func(b *testing.B) {
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
		b.Run(name, func(b *testing.B) {
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
	})
}

// Context returns the CI Visibility context of the Test span.
// This may be used to create test's children spans useful for
// integration tests.
func (ddb *B) Context() context.Context {
	b := (*testing.B)(ddb)
	ciTest := getCiVisibilityBenchmark(b)
	if ciTest != nil {
		return ciTest.Context()
	}

	return context.Background()
}

// Fail marks the function as having failed but continues execution.
func (ddb *B) Fail() { ddb.getBWithError("Fail", "failed test").Fail() }

// FailNow marks the function as having failed and stops its execution
// by calling runtime.Goexit (which then runs all deferred calls in the
// current goroutine). Execution will continue at the next test or benchmark.
// FailNow must be called from the goroutine running the test or benchmark function,
// not from other goroutines created during the test. Calling FailNow does not stop
// those other goroutines.
func (ddb *B) FailNow() {
	b := ddb.getBWithError("FailNow", "failed test")
	integrations.ExitCiVisibility()
	b.FailNow()
}

// Error is equivalent to Log followed by Fail.
func (ddb *B) Error(args ...any) { ddb.getBWithError("Error", fmt.Sprint(args...)).Error(args...) }

// Errorf is equivalent to Logf followed by Fail.
func (ddb *B) Errorf(format string, args ...any) {
	ddb.getBWithError("Errorf", fmt.Sprintf(format, args...)).Errorf(format, args...)
}

// Fatal is equivalent to Log followed by FailNow.
func (ddb *B) Fatal(args ...any) { ddb.getBWithError("Fatal", fmt.Sprint(args...)).Fatal(args...) }

// Fatalf is equivalent to Logf followed by FailNow.
func (ddb *B) Fatalf(format string, args ...any) {
	ddb.getBWithError("Fatalf", fmt.Sprintf(format, args...)).Fatalf(format, args...)
}

// Skip is equivalent to Log followed by SkipNow.
func (ddb *B) Skip(args ...any) { ddb.getBWithSkip(fmt.Sprint(args...)).Skip(args...) }

// Skipf is equivalent to Logf followed by SkipNow.
func (ddb *B) Skipf(format string, args ...any) {
	ddb.getBWithSkip(fmt.Sprintf(format, args...)).Skipf(format, args...)
}

// SkipNow marks the test as having been skipped and stops its execution
// by calling runtime.Goexit. If a test fails (see Error, Errorf, Fail) and is then skipped,
// it is still considered to have failed. Execution will continue at the next test or benchmark.
// SkipNow must be called from the goroutine running the test, not from other goroutines created
// during the test. Calling SkipNow does not stop those other goroutines.
func (ddb *B) SkipNow() {
	b := (*testing.B)(ddb)
	ciTest := getCiVisibilityBenchmark(b)
	if ciTest != nil {
		ciTest.Close(integrations.ResultStatusSkip)
	}

	b.SkipNow()
}

// StartTimer starts timing a test. This function is called automatically
// before a benchmark starts, but it can also be used to resume timing after
// a call to StopTimer.
func (ddb *B) StartTimer() { (*testing.B)(ddb).StartTimer() }

// StopTimer stops timing a test. This can be used to pause the timer
// while performing complex initialization that you don't want to measure.
func (ddb *B) StopTimer() { (*testing.B)(ddb).StopTimer() }

// ReportAllocs enables malloc statistics for this benchmark.
// It is equivalent to setting -test.benchmem, but it only affects the
// benchmark function that calls ReportAllocs.
func (ddb *B) ReportAllocs() { (*testing.B)(ddb).ReportAllocs() }

// ResetTimer zeroes the elapsed benchmark time and memory allocation counters
// and deletes user-reported metrics. It does not affect whether the timer is running.
func (ddb *B) ResetTimer() { (*testing.B)(ddb).ResetTimer() }

// Elapsed returns the measured elapsed time of the benchmark.
// The duration reported by Elapsed matches the one measured by
// StartTimer, StopTimer, and ResetTimer.
func (ddb *B) Elapsed() time.Duration {
	return (*testing.B)(ddb).Elapsed()
}

// ReportMetric adds "n unit" to the reported benchmark results.
// If the metric is per-iteration, the caller should divide by b.N,
// and by convention units should end in "/op".
// ReportMetric overrides any previously reported value for the same unit.
// ReportMetric panics if unit is the empty string or if unit contains
// any whitespace.
// If unit is a unit normally reported by the benchmark framework itself
// (such as "allocs/op"), ReportMetric will override that metric.
// Setting "ns/op" to 0 will suppress that built-in metric.
func (ddb *B) ReportMetric(n float64, unit string) { (*testing.B)(ddb).ReportMetric(n, unit) }

// RunParallel runs a benchmark in parallel.
// It creates multiple goroutines and distributes b.N iterations among them.
// The number of goroutines defaults to GOMAXPROCS. To increase parallelism for
// non-CPU-bound benchmarks, call SetParallelism before RunParallel.
// RunParallel is usually used with the go test -cpu flag.
//
// The body function will be run in each goroutine. It should set up any
// goroutine-local state and then iterate until pb.Next returns false.
// It should not use the StartTimer, StopTimer, or ResetTimer functions,
// because they have global effect. It should also not call Run.
//
// RunParallel reports ns/op values as wall time for the benchmark as a whole,
// not the sum of wall time or CPU time over each parallel goroutine.
func (ddb *B) RunParallel(body func(*testing.PB)) { (*testing.B)(ddb).RunParallel(body) }

// SetBytes records the number of bytes processed in a single operation.
// If this is called, the benchmark will report ns/op and MB/s.
func (ddb *B) SetBytes(n int64) { (*testing.B)(ddb).SetBytes(n) }

// SetParallelism sets the number of goroutines used by RunParallel to p*GOMAXPROCS.
// There is usually no need to call SetParallelism for CPU-bound benchmarks.
// If p is less than 1, this call will have no effect.
func (ddb *B) SetParallelism(p int) { (*testing.B)(ddb).SetParallelism(p) }

func (ddb *B) getBWithError(errType string, errMessage string) *testing.B {
	b := (*testing.B)(ddb)
	ciTest := getCiVisibilityBenchmark(b)
	if ciTest != nil {
		ciTest.SetErrorInfo(errType, errMessage, utils.GetStacktrace(2))
	}
	return b
}

func (ddb *B) getBWithSkip(skipReason string) *testing.B {
	b := (*testing.B)(ddb)
	ciTest := getCiVisibilityBenchmark(b)
	if ciTest != nil {
		ciTest.CloseWithFinishTimeAndSkipReason(integrations.ResultStatusSkip, time.Now(), skipReason)
	}
	return b
}

// getCiVisibilityBenchmark retrieves the CI visibility benchmark associated with a given *testing.B.
func getCiVisibilityBenchmark(b *testing.B) integrations.DdTest {
	ciVisibilityBenchmarksMutex.RLock()
	defer ciVisibilityBenchmarksMutex.RUnlock()

	if v, ok := ciVisibilityBenchmarks[b]; ok {
		return v
	}

	return nil
}

// setCiVisibilityBenchmark associates a CI visibility benchmark with a given *testing.B.
func setCiVisibilityBenchmark(b *testing.B, ciTest integrations.DdTest) {
	ciVisibilityBenchmarksMutex.Lock()
	defer ciVisibilityBenchmarksMutex.Unlock()
	ciVisibilityBenchmarks[b] = ciTest
}

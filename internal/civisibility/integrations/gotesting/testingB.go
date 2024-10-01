// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gotesting

import (
	"context"
	"fmt"
	"regexp"
	"runtime"
	"sync"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations"
)

var (
	// subBenchmarkAutoName is a placeholder name for CI Visibility sub-benchmarks.
	subBenchmarkAutoName = "[DD:TestVisibility]"

	// subBenchmarkAutoNameRegex is a regex pattern to match the sub-benchmark auto name.
	subBenchmarkAutoNameRegex = regexp.MustCompile(`(?si)\/\[DD:TestVisibility\].*`)

	// civisibilityBenchmarksFuncs holds a map of *runtime.Func for tracking instrumented functions
	civisibilityBenchmarksFuncs = map[*runtime.Func]struct{}{}

	// civisibilityBenchmarksFuncsMutex is a read-write mutex for synchronizing access to civisibilityBenchmarksFuncs.
	civisibilityBenchmarksFuncsMutex sync.RWMutex
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
	pb := (*testing.B)(ddb)
	name, f = instrumentTestingBFunc(pb, name, f)
	return pb.Run(name, f)
}

// Context returns the CI Visibility context of the Test span.
// This may be used to create test's children spans useful for
// integration tests.
func (ddb *B) Context() context.Context {
	b := (*testing.B)(ddb)
	ciTestItem := getTestMetadata(b)
	if ciTestItem != nil && ciTestItem.test != nil {
		return ciTestItem.test.Context()
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
	instrumentSkipNow(b)
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
	instrumentSetErrorInfo(b, errType, errMessage, 1)
	return b
}

func (ddb *B) getBWithSkip(skipReason string) *testing.B {
	b := (*testing.B)(ddb)
	instrumentCloseAndSkip(b, skipReason)
	return b
}

// hasCiVisibilityBenchmarkFunc gets if a *runtime.Func is being instrumented.
func hasCiVisibilityBenchmarkFunc(fn *runtime.Func) bool {
	civisibilityBenchmarksFuncsMutex.RLock()
	defer civisibilityBenchmarksFuncsMutex.RUnlock()

	if _, ok := civisibilityBenchmarksFuncs[fn]; ok {
		return true
	}

	return false
}

// setCiVisibilityBenchmarkFunc tracks a *runtime.Func as instrumented benchmark.
func setCiVisibilityBenchmarkFunc(fn *runtime.Func) {
	civisibilityBenchmarksFuncsMutex.RLock()
	defer civisibilityBenchmarksFuncsMutex.RUnlock()
	civisibilityBenchmarksFuncs[fn] = struct{}{}
}

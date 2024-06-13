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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils"
)

var (
	// ciVisibilityTests holds a map of *testing.T to civisibility.DdTest for tracking tests.
	ciVisibilityTests = map[*testing.T]integrations.DdTest{}

	// ciVisibilityTestsMutex is a read-write mutex for synchronizing access to ciVisibilityTests.
	ciVisibilityTestsMutex sync.RWMutex
)

// T is a type alias for testing.T to provide additional methods for CI visibility.
type T testing.T

// GetTest is a helper to return *gotesting.T from *testing.T.
// Internally, it is just a (*gotesting.T)(t) cast.
func GetTest(t *testing.T) *T {
	return (*T)(t)
}

// Run runs f as a subtest of t called name. It runs f in a separate goroutine
// and blocks until f returns or calls t.Parallel to become a parallel test.
// Run reports whether f succeeded (or at least did not fail before calling t.Parallel).
//
// Run may be called simultaneously from multiple goroutines, but all such calls
// must return before the outer test function for t returns.
func (ddt *T) Run(name string, f func(*testing.T)) bool {
	// Reflect the function to obtain its pointer.
	fReflect := reflect.Indirect(reflect.ValueOf(f))
	moduleName, suiteName := utils.GetModuleAndSuiteName(fReflect.Pointer())
	originalFunc := runtime.FuncForPC(fReflect.Pointer())

	// Increment the test count in the module.
	atomic.AddInt32(modulesCounters[moduleName], 1)

	// Increment the test count in the suite.
	atomic.AddInt32(suitesCounters[suiteName], 1)

	t := (*testing.T)(ddt)
	return t.Run(name, func(t *testing.T) {
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
	})
}

// Context returns the CI Visibility context of the Test span.
// This may be used to create test's children spans useful for
// integration tests.
func (ddt *T) Context() context.Context {
	t := (*testing.T)(ddt)
	ciTest := getCiVisibilityTest(t)
	if ciTest != nil {
		return ciTest.Context()
	}

	return context.Background()
}

// Fail marks the function as having failed but continues execution.
func (ddt *T) Fail() { ddt.getTWithError("Fail", "failed test").Fail() }

// FailNow marks the function as having failed and stops its execution
// by calling runtime.Goexit (which then runs all deferred calls in the
// current goroutine). Execution will continue at the next test or benchmark.
// FailNow must be called from the goroutine running the test or benchmark function,
// not from other goroutines created during the test. Calling FailNow does not stop
// those other goroutines.
func (ddt *T) FailNow() {
	t := ddt.getTWithError("FailNow", "failed test")
	integrations.ExitCiVisibility()
	t.FailNow()
}

// Error is equivalent to Log followed by Fail.
func (ddt *T) Error(args ...any) { ddt.getTWithError("Error", fmt.Sprint(args...)).Error(args...) }

// Errorf is equivalent to Logf followed by Fail.
func (ddt *T) Errorf(format string, args ...any) {
	ddt.getTWithError("Errorf", fmt.Sprintf(format, args...)).Errorf(format, args...)
}

// Fatal is equivalent to Log followed by FailNow.
func (ddt *T) Fatal(args ...any) { ddt.getTWithError("Fatal", fmt.Sprint(args...)).Fatal(args...) }

// Fatalf is equivalent to Logf followed by FailNow.
func (ddt *T) Fatalf(format string, args ...any) {
	ddt.getTWithError("Fatalf", fmt.Sprintf(format, args...)).Fatalf(format, args...)
}

// Skip is equivalent to Log followed by SkipNow.
func (ddt *T) Skip(args ...any) { ddt.getTWithSkip(fmt.Sprint(args...)).Skip(args...) }

// Skipf is equivalent to Logf followed by SkipNow.
func (ddt *T) Skipf(format string, args ...any) {
	ddt.getTWithSkip(fmt.Sprintf(format, args...)).Skipf(format, args...)
}

// SkipNow marks the test as having been skipped and stops its execution
// by calling runtime.Goexit. If a test fails (see Error, Errorf, Fail) and is then skipped,
// it is still considered to have failed. Execution will continue at the next test or benchmark.
// SkipNow must be called from the goroutine running the test, not from other goroutines created
// during the test. Calling SkipNow does not stop those other goroutines.
func (ddt *T) SkipNow() {
	t := (*testing.T)(ddt)
	ciTest := getCiVisibilityTest(t)
	if ciTest != nil {
		ciTest.Close(integrations.ResultStatusSkip)
	}

	t.SkipNow()
}

// Parallel signals that this test is to be run in parallel with (and only with)
// other parallel tests. When a test is run multiple times due to use of
// -test.count or -test.cpu, multiple instances of a single test never run in
// parallel with each other.
func (ddt *T) Parallel() { (*testing.T)(ddt).Parallel() }

// Deadline reports the time at which the test binary will have
// exceeded the timeout specified by the -timeout flag.
// The ok result is false if the -timeout flag indicates “no timeout” (0).
func (ddt *T) Deadline() (deadline time.Time, ok bool) {
	return (*testing.T)(ddt).Deadline()
}

// Setenv calls os.Setenv(key, value) and uses Cleanup to
// restore the environment variable to its original value
// after the test. Because Setenv affects the whole process,
// it cannot be used in parallel tests or tests with parallel ancestors.
func (ddt *T) Setenv(key, value string) { (*testing.T)(ddt).Setenv(key, value) }

func (ddt *T) getTWithError(errType string, errMessage string) *testing.T {
	t := (*testing.T)(ddt)
	ciTest := getCiVisibilityTest(t)
	if ciTest != nil {
		ciTest.SetErrorInfo(errType, errMessage, utils.GetStacktrace(2))
	}
	return t
}

func (ddt *T) getTWithSkip(skipReason string) *testing.T {
	t := (*testing.T)(ddt)
	ciTest := getCiVisibilityTest(t)
	if ciTest != nil {
		ciTest.CloseWithFinishTimeAndSkipReason(integrations.ResultStatusSkip, time.Now(), skipReason)
	}
	return t
}

// getCiVisibilityTest retrieves the CI visibility test associated with a given *testing.T.
func getCiVisibilityTest(t *testing.T) integrations.DdTest {
	ciVisibilityTestsMutex.RLock()
	defer ciVisibilityTestsMutex.RUnlock()

	if v, ok := ciVisibilityTests[t]; ok {
		return v
	}

	return nil
}

// setCiVisibilityTest associates a CI visibility test with a given *testing.T.
func setCiVisibilityTest(t *testing.T, ciTest integrations.DdTest) {
	ciVisibilityTestsMutex.Lock()
	defer ciVisibilityTestsMutex.Unlock()
	ciVisibilityTests[t] = ciTest
}

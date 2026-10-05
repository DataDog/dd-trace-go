// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gotesting

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// T is a type alias for testing.T to provide additional methods for CI visibility.
type T testing.T

type singleEvaluationMessage struct {
	once      sync.Once
	format    string
	args      []any
	println   bool
	value     string
	formatted bool
}

func (m *singleEvaluationMessage) String() string {
	m.once.Do(func() {
		if m.println {
			m.value = strings.TrimSuffix(fmt.Sprintln(m.args...), "\n")
		} else {
			m.value = fmt.Sprintf(m.format, m.args...)
		}
		m.formatted = true
	})
	return m.value
}

func (m *singleEvaluationMessage) captured() (string, bool) {
	return m.value, m.formatted
}

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
	f = instrumentTestingTFunc(f)
	t := (*testing.T)(ddt)
	return t.Run(name, f)
}

// Context returns the CI Visibility context of the Test span.
// This may be used to create test's children spans useful for
// integration tests.
func (ddt *T) Context() context.Context {
	t := (*testing.T)(ddt)
	return getTestOptimizationContext(t)
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
	t.FailNow()
}

// Error is equivalent to Log followed by Fail.
func (ddt *T) Error(args ...any) {
	t := (*testing.T)(ddt)
	t.Helper()
	message := &singleEvaluationMessage{args: args, println: true}
	t.Error(message)
	if value, ok := message.captured(); ok {
		instrumentSetErrorInfo(t, "Error", value, 1)
	}
}

// Errorf is equivalent to Logf followed by Fail.
func (ddt *T) Errorf(format string, args ...any) {
	t := (*testing.T)(ddt)
	t.Helper()
	message := &singleEvaluationMessage{format: format, args: args}
	t.Errorf("%s", message)
	if value, ok := message.captured(); ok {
		instrumentSetErrorInfo(t, "Errorf", value, 1)
	}
}

// Fatal is equivalent to Log followed by FailNow.
func (ddt *T) Fatal(args ...any) {
	t := (*testing.T)(ddt)
	t.Helper()
	message := &singleEvaluationMessage{args: args, println: true}
	defer func() {
		if value, ok := message.captured(); ok && t.Failed() && testingTFinished(t) {
			instrumentSetErrorInfo(t, "Fatal", value, 1)
		}
	}()
	t.Fatal(message)
}

// Fatalf is equivalent to Logf followed by FailNow.
func (ddt *T) Fatalf(format string, args ...any) {
	t := (*testing.T)(ddt)
	t.Helper()
	message := &singleEvaluationMessage{format: format, args: args}
	defer func() {
		if value, ok := message.captured(); ok && t.Failed() && testingTFinished(t) {
			instrumentSetErrorInfo(t, "Fatalf", value, 1)
		}
	}()
	t.Fatalf("%s", message)
}

// Skip is equivalent to Log followed by SkipNow.
func (ddt *T) Skip(args ...any) {
	t := (*testing.T)(ddt)
	t.Helper()
	message := &singleEvaluationMessage{args: args, println: true}
	defer func() {
		if value, ok := message.captured(); ok && t.Skipped() && testingTFinished(t) {
			instrumentCloseAndSkip(t, value)
		}
	}()
	t.Skip(message)
}

// Skipf is equivalent to Logf followed by SkipNow.
func (ddt *T) Skipf(format string, args ...any) {
	t := (*testing.T)(ddt)
	t.Helper()
	message := &singleEvaluationMessage{format: format, args: args}
	defer func() {
		if value, ok := message.captured(); ok && t.Skipped() && testingTFinished(t) {
			instrumentCloseAndSkip(t, value)
		}
	}()
	t.Skipf("%s", message)
}

// SkipNow marks the test as having been skipped and stops its execution
// by calling runtime.Goexit. If a test fails (see Error, Errorf, Fail) and is then skipped,
// it is still considered to have failed. Execution will continue at the next test or benchmark.
// SkipNow must be called from the goroutine running the test, not from other goroutines created
// during the test. Calling SkipNow does not stop those other goroutines.
func (ddt *T) SkipNow() {
	t := (*testing.T)(ddt)
	instrumentSkipNow(t)
	t.SkipNow()
}

// Parallel signals that this test is to be run in parallel with (and only with)
// other parallel tests. When a test is run multiple times due to use of
// -test.count or -test.cpu, multiple instances of a single test never run in
// parallel with each other.
func (ddt *T) Parallel() {
	t := (*testing.T)(ddt)
	if !instrumentTestingParallel(t) {
		t.Parallel()
	}
}

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
	instrumentSetErrorInfo(t, errType, errMessage, 1)
	return t
}

func testingTFinished(t *testing.T) bool {
	return shouldCaptureTerminalMessage(getTestPrivateFields(t))
}

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gotesting

import (
	"errors"
	"io"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
)

// getFieldPointerFrom gets an unsafe.Pointer (gc-safe type of pointer) to a struct field
// useful to get or set values to private field
func getFieldPointerFrom(value any, fieldName string) (unsafe.Pointer, error) {
	return getFieldPointerFromValue(reflect.Indirect(reflect.ValueOf(value)), fieldName)
}

// getFieldPointerFromValue gets an unsafe.Pointer (gc-safe type of pointer) to a struct field
// useful to get or set values to private field
func getFieldPointerFromValue(value reflect.Value, fieldName string) (unsafe.Pointer, error) {
	member := value.FieldByName(fieldName)
	if member.IsValid() {
		return unsafe.Pointer(member.UnsafeAddr()), nil
	}

	return unsafe.Pointer(nil), errors.New("member is invalid")
}

// copyFieldUsingPointers copies a private field value from one struct to another of the same type
func copyFieldUsingPointers[V any](source any, target any, fieldName string) error {
	sourcePtr, err := getFieldPointerFrom(source, fieldName)
	if err != nil {
		return err
	}
	targetPtr, err := getFieldPointerFrom(target, fieldName)
	if err != nil {
		return err
	}

	if targetPtr == nil {
		return errors.New("target pointer is nil")
	}

	if sourcePtr == nil {
		return errors.New("source pointer is nil")
	}

	if (*V)(targetPtr) == nil {
		return errors.New("target pointer value is nil")
	}

	if (*V)(sourcePtr) == nil {
		return errors.New("source pointer value is nil")
	}

	*(*V)(targetPtr) = *(*V)(sourcePtr)
	return nil
}

// ****************
// COMMON
// ****************

// commonPrivateFields is collection of required private fields from testing.common
type commonPrivateFields struct {
	mu      *sync.RWMutex
	level   *int
	name    *string         // Name of test or benchmark.
	failed  *bool           // Test or benchmark has failed.
	skipped *bool           // Test or benchmark has been skipped.
	parent  *unsafe.Pointer // Parent common
	barrier *chan bool      // Barrier for parallel tests
}

// AddLevel increase or decrease the testing.common.level field value, used by
// testing.B to create the name of the benchmark test
func (c *commonPrivateFields) AddLevel(delta int) int {
	if c.mu == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.level == nil {
		return 0
	}
	*c.level = *c.level + delta
	return *c.level
}

// SetFailed set the boolean value in testing.common.failed field value
func (c *commonPrivateFields) SetFailed(value bool) {
	if c.mu == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failed == nil {
		return
	}
	*c.failed = value
}

// SetSkipped set the boolean value in testing.common.skipped field value
func (c *commonPrivateFields) SetSkipped(value bool) {
	if c.mu == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.skipped == nil {
		return
	}
	*c.skipped = value
}

// ****************
// TESTING
// ****************

// getInternalTestArray gets the pointer to the testing.InternalTest array inside a
// testing.M instance containing all the "root" tests
func getInternalTestArray(m *testing.M) *[]testing.InternalTest {
	if ptr, err := getFieldPointerFrom(m, "tests"); err == nil && ptr != nil {
		return (*[]testing.InternalTest)(ptr)
	}
	return nil
}

// getTestPrivateFields is a method to retrieve all required privates field from
// testing.T, returning a commonPrivateFields instance
func getTestPrivateFields(t *testing.T) *commonPrivateFields {
	testFields := &commonPrivateFields{}

	// testing.common
	if ptr, err := getFieldPointerFrom(t, "mu"); err == nil && ptr != nil {
		testFields.mu = (*sync.RWMutex)(ptr)
	}
	if ptr, err := getFieldPointerFrom(t, "level"); err == nil && ptr != nil {
		testFields.level = (*int)(ptr)
	}
	if ptr, err := getFieldPointerFrom(t, "name"); err == nil && ptr != nil {
		testFields.name = (*string)(ptr)
	}
	if ptr, err := getFieldPointerFrom(t, "failed"); err == nil && ptr != nil {
		testFields.failed = (*bool)(ptr)
	}
	if ptr, err := getFieldPointerFrom(t, "skipped"); err == nil && ptr != nil {
		testFields.skipped = (*bool)(ptr)
	}
	if ptr, err := getFieldPointerFrom(t, "parent"); err == nil && ptr != nil {
		testFields.parent = (*unsafe.Pointer)(ptr)
	}
	if ptr, err := getFieldPointerFrom(t, "barrier"); err == nil {
		testFields.barrier = (*chan bool)(ptr)
	}

	return testFields
}

// getTestParentPrivateFields is a method to retrieve all required parent privates field from
// testing.T.parent, returning a commonPrivateFields instance
func getTestParentPrivateFields(t *testing.T) *commonPrivateFields {
	indirectValue := reflect.Indirect(reflect.ValueOf(t))
	member := indirectValue.FieldByName("parent")
	if member.IsValid() {
		value := member.Elem()
		testFields := &commonPrivateFields{}

		// testing.common
		if ptr, err := getFieldPointerFromValue(value, "mu"); err == nil && ptr != nil {
			testFields.mu = (*sync.RWMutex)(ptr)
		}
		if ptr, err := getFieldPointerFromValue(value, "level"); err == nil && ptr != nil {
			testFields.level = (*int)(ptr)
		}
		if ptr, err := getFieldPointerFromValue(value, "name"); err == nil && ptr != nil {
			testFields.name = (*string)(ptr)
		}
		if ptr, err := getFieldPointerFromValue(value, "failed"); err == nil && ptr != nil {
			testFields.failed = (*bool)(ptr)
		}
		if ptr, err := getFieldPointerFromValue(value, "skipped"); err == nil && ptr != nil {
			testFields.skipped = (*bool)(ptr)
		}
		if ptr, err := getFieldPointerFromValue(value, "barrier"); err == nil {
			testFields.barrier = (*chan bool)(ptr)
		}

		return testFields
	}
	return nil
}

// contextMatcher is collection of required private fields from testing.context.match
type contextMatcher struct {
	mu       *sync.RWMutex
	subNames *map[string]int32
}

// ClearSubNames clears the subname map used for creating unique names for subtests
func (c *contextMatcher) ClearSubNames() {
	if c.mu == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.subNames == nil {
		return
	}
	*c.subNames = map[string]int32{}
}

// getTestContextMatcherPrivateFields is a method to retrieve all required privates field from
// testing.T.context.match, returning a contextMatcher instance
func getTestContextMatcherPrivateFields(t *testing.T) *contextMatcher {
	indirectValue := reflect.Indirect(reflect.ValueOf(t))
	contextMember := indirectValue.FieldByName("context")
	if !contextMember.IsValid() {
		return nil
	}
	contextMember = contextMember.Elem()
	matchMember := contextMember.FieldByName("match")
	if !matchMember.IsValid() {
		return nil
	}
	matchMember = matchMember.Elem()

	fields := &contextMatcher{}
	if ptr, err := getFieldPointerFromValue(matchMember, "mu"); err == nil && ptr != nil {
		fields.mu = (*sync.RWMutex)(ptr)
	}
	if ptr, err := getFieldPointerFromValue(matchMember, "subNames"); err == nil && ptr != nil {
		fields.subNames = (*map[string]int32)(ptr)
	}

	return fields
}

// copyTestWithoutParent tries to copy all private fields except the t.parent from a *testing.T to another
func copyTestWithoutParent(source *testing.T, target *testing.T) {
	// Copy important field values
	_ = copyFieldUsingPointers[[]byte](source, target, "output")                   // Output generated by test or benchmark.
	_ = copyFieldUsingPointers[io.Writer](source, target, "w")                     // For flushToParent.
	_ = copyFieldUsingPointers[bool](source, target, "ran")                        // Test or benchmark (or one of its subtests) was executed.
	_ = copyFieldUsingPointers[bool](source, target, "failed")                     // Test or benchmark has failed.
	_ = copyFieldUsingPointers[bool](source, target, "skipped")                    // Test or benchmark has been skipped.
	_ = copyFieldUsingPointers[bool](source, target, "done")                       // Test is finished and all subtests have completed.
	_ = copyFieldUsingPointers[map[uintptr]struct{}](source, target, "helperPCs")  // functions to be skipped when writing file/line info
	_ = copyFieldUsingPointers[map[string]struct{}](source, target, "helperNames") // helperPCs converted to function names
	_ = copyFieldUsingPointers[[]func()](source, target, "cleanups")               // optional functions to be called at the end of the test
	_ = copyFieldUsingPointers[string](source, target, "cleanupName")              // Name of the cleanup function.
	_ = copyFieldUsingPointers[[]uintptr](source, target, "cleanupPc")             // The stack trace at the point where Cleanup was called.
	_ = copyFieldUsingPointers[bool](source, target, "finished")                   // Test function has completed.
	_ = copyFieldUsingPointers[bool](source, target, "inFuzzFn")                   // Whether the fuzz target, if this is one, is running.

	_ = copyFieldUsingPointers[unsafe.Pointer](source, target, "chatty")      // A copy of chattyPrinter, if the chatty flag is set.
	_ = copyFieldUsingPointers[bool](source, target, "bench")                 // Whether the current test is a benchmark.
	_ = copyFieldUsingPointers[atomic.Bool](source, target, "hasSub")         // whether there are sub-benchmarks.
	_ = copyFieldUsingPointers[atomic.Bool](source, target, "cleanupStarted") // Registered cleanup callbacks have started to execute
	_ = copyFieldUsingPointers[string](source, target, "runner")              // Function name of tRunner running the test.
	_ = copyFieldUsingPointers[bool](source, target, "isParallel")            // Whether the test is parallel.

	_ = copyFieldUsingPointers[int](source, target, "level")            // Nesting depth of test or benchmark.
	_ = copyFieldUsingPointers[[]uintptr](source, target, "creator")    // If level > 0, the stack trace at the point where the parent called t.Run.
	_ = copyFieldUsingPointers[string](source, target, "name")          // Name of test or benchmark.
	_ = copyFieldUsingPointers[unsafe.Pointer](source, target, "start") // Time test or benchmark started
	_ = copyFieldUsingPointers[time.Duration](source, target, "duration")
	_ = copyFieldUsingPointers[[]*testing.T](source, target, "sub")            // Queue of subtests to be run in parallel.
	_ = copyFieldUsingPointers[atomic.Int64](source, target, "lastRaceErrors") // Max value of race.Errors seen during the test or its subtests.
	_ = copyFieldUsingPointers[atomic.Bool](source, target, "raceErrorLogged")
	_ = copyFieldUsingPointers[string](source, target, "tempDir")
	_ = copyFieldUsingPointers[error](source, target, "tempDirErr")
	_ = copyFieldUsingPointers[int32](source, target, "tempDirSeq")

	_ = copyFieldUsingPointers[bool](source, target, "isEnvSet")
	_ = copyFieldUsingPointers[unsafe.Pointer](source, target, "context") // For running tests and subtests.
}

// ****************
// BENCHMARKS
// ****************

// get the pointer to the internal benchmark array
// getInternalBenchmarkArray gets the pointer to the testing.InternalBenchmark array inside
// a testing.M instance containing all the "root" benchmarks
func getInternalBenchmarkArray(m *testing.M) *[]testing.InternalBenchmark {
	if ptr, err := getFieldPointerFrom(m, "benchmarks"); err == nil && ptr != nil {
		return (*[]testing.InternalBenchmark)(ptr)
	}
	return nil
}

// benchmarkPrivateFields is a collection of required private fields from testing.B
// also contains a pointer to the original testing.B for easy access
type benchmarkPrivateFields struct {
	commonPrivateFields
	B         *testing.B
	benchFunc *func(b *testing.B)
	result    *testing.BenchmarkResult
}

// getBenchmarkPrivateFields is a method to retrieve all required privates field from
// testing.B, returning a benchmarkPrivateFields instance
func getBenchmarkPrivateFields(b *testing.B) *benchmarkPrivateFields {
	benchFields := &benchmarkPrivateFields{
		B: b,
	}

	// testing.common
	if ptr, err := getFieldPointerFrom(b, "mu"); err == nil && ptr != nil {
		benchFields.mu = (*sync.RWMutex)(ptr)
	}
	if ptr, err := getFieldPointerFrom(b, "level"); err == nil && ptr != nil {
		benchFields.level = (*int)(ptr)
	}
	if ptr, err := getFieldPointerFrom(b, "name"); err == nil && ptr != nil {
		benchFields.name = (*string)(ptr)
	}
	if ptr, err := getFieldPointerFrom(b, "failed"); err == nil && ptr != nil {
		benchFields.failed = (*bool)(ptr)
	}
	if ptr, err := getFieldPointerFrom(b, "skipped"); err == nil && ptr != nil {
		benchFields.skipped = (*bool)(ptr)
	}
	if ptr, err := getFieldPointerFrom(b, "parent"); err == nil && ptr != nil {
		benchFields.parent = (*unsafe.Pointer)(ptr)
	}

	// testing.B
	if ptr, err := getFieldPointerFrom(b, "benchFunc"); err == nil && ptr != nil {
		benchFields.benchFunc = (*func(b *testing.B))(ptr)
	}
	if ptr, err := getFieldPointerFrom(b, "result"); err == nil && ptr != nil {
		benchFields.result = (*testing.BenchmarkResult)(ptr)
	}

	return benchFields
}

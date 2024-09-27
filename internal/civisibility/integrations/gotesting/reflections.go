// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gotesting

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"unsafe"
)

// getFieldPointerFrom gets an unsafe.Pointer (gc-safe type of pointer) to a struct field
// useful to get or set values to private field
func getFieldPointerFrom(value any, fieldName string) (unsafe.Pointer, error) {
	indirectValue := reflect.Indirect(reflect.ValueOf(value))
	member := indirectValue.FieldByName(fieldName)
	if member.IsValid() {
		return unsafe.Pointer(member.UnsafeAddr()), nil
	}

	return unsafe.Pointer(nil), errors.New("member is invalid")
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

// COMMON

// commonPrivateFields is collection of required private fields from testing.common
type commonPrivateFields struct {
	mu       *sync.RWMutex
	level    *int
	name     *string // Name of test or benchmark.
	ran      *bool   // Test or benchmark (or one of its subtests) was executed.
	failed   *bool   // Test or benchmark has failed.
	skipped  *bool   // Test or benchmark has been skipped.
	done     *bool   // Test is finished and all subtests have completed.
	finished *bool   // Test function has completed.
}

// AddLevel increase or decrease the testing.common.level field value, used by
// testing.B to create the name of the benchmark test
func (c *commonPrivateFields) AddLevel(delta int) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	*c.level = *c.level + delta
	return *c.level
}

// SetRan set the boolean value in testing.common.ran field value
func (c *commonPrivateFields) SetRan(value bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	*c.ran = value
}

// SetFailed set the boolean value in testing.common.failed field value
func (c *commonPrivateFields) SetFailed(value bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	*c.failed = value
}

// SetSkipped set the boolean value in testing.common.skipped field value
func (c *commonPrivateFields) SetSkipped(value bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	*c.skipped = value
}

// SetDone set the boolean value in testing.common.done field value
func (c *commonPrivateFields) SetDone(value bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	*c.done = value
}

// SetFinished set the boolean value in testing.common.finished field value
func (c *commonPrivateFields) SetFinished(value bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	*c.finished = value
}

// TESTING

// getInternalTestArray gets the pointer to the testing.InternalTest array inside a
// testing.M instance containing all the "root" tests
func getInternalTestArray(m *testing.M) *[]testing.InternalTest {
	if ptr, err := getFieldPointerFrom(m, "tests"); err == nil {
		return (*[]testing.InternalTest)(ptr)
	}
	return nil
}

func getTestPrivateFields(t *testing.T) *commonPrivateFields {
	testFields := &commonPrivateFields{}

	// common
	if ptr, err := getFieldPointerFrom(t, "mu"); err == nil {
		testFields.mu = (*sync.RWMutex)(ptr)
	}
	if ptr, err := getFieldPointerFrom(t, "level"); err == nil {
		testFields.level = (*int)(ptr)
	}
	if ptr, err := getFieldPointerFrom(t, "name"); err == nil {
		testFields.name = (*string)(ptr)
	}
	if ptr, err := getFieldPointerFrom(t, "ran"); err == nil {
		testFields.ran = (*bool)(ptr)
	}
	if ptr, err := getFieldPointerFrom(t, "failed"); err == nil {
		testFields.failed = (*bool)(ptr)
	}
	if ptr, err := getFieldPointerFrom(t, "skipped"); err == nil {
		testFields.skipped = (*bool)(ptr)
	}
	if ptr, err := getFieldPointerFrom(t, "done"); err == nil {
		testFields.done = (*bool)(ptr)
	}
	if ptr, err := getFieldPointerFrom(t, "finished"); err == nil {
		testFields.finished = (*bool)(ptr)
	}

	return testFields
}

func getTestPrivateFieldsFromReflection(value reflect.Value) *commonPrivateFields {
	testFields := &commonPrivateFields{}

	// common
	if ptr, err := getFieldPointerFromValue(value, "mu"); err == nil {
		testFields.mu = (*sync.RWMutex)(ptr)
	}
	if ptr, err := getFieldPointerFromValue(value, "level"); err == nil {
		testFields.level = (*int)(ptr)
	}
	if ptr, err := getFieldPointerFromValue(value, "name"); err == nil {
		testFields.name = (*string)(ptr)
	}
	if ptr, err := getFieldPointerFromValue(value, "ran"); err == nil {
		testFields.ran = (*bool)(ptr)
	}
	if ptr, err := getFieldPointerFromValue(value, "failed"); err == nil {
		testFields.failed = (*bool)(ptr)
	}
	if ptr, err := getFieldPointerFromValue(value, "skipped"); err == nil {
		testFields.skipped = (*bool)(ptr)
	}
	if ptr, err := getFieldPointerFromValue(value, "done"); err == nil {
		testFields.done = (*bool)(ptr)
	}
	if ptr, err := getFieldPointerFromValue(value, "finished"); err == nil {
		testFields.finished = (*bool)(ptr)
	}

	return testFields
}

// BENCHMARKS

// get the pointer to the internal benchmark array
// getInternalBenchmarkArray gets the pointer to the testing.InternalBenchmark array inside
// a testing.M instance containing all the "root" benchmarks
func getInternalBenchmarkArray(m *testing.M) *[]testing.InternalBenchmark {
	if ptr, err := getFieldPointerFrom(m, "benchmarks"); err == nil {
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

	// common
	if ptr, err := getFieldPointerFrom(b, "mu"); err == nil {
		benchFields.mu = (*sync.RWMutex)(ptr)
	}
	if ptr, err := getFieldPointerFrom(b, "level"); err == nil {
		benchFields.level = (*int)(ptr)
	}
	if ptr, err := getFieldPointerFrom(b, "name"); err == nil {
		benchFields.name = (*string)(ptr)
	}
	if ptr, err := getFieldPointerFrom(b, "ran"); err == nil {
		benchFields.ran = (*bool)(ptr)
	}
	if ptr, err := getFieldPointerFrom(b, "failed"); err == nil {
		benchFields.failed = (*bool)(ptr)
	}
	if ptr, err := getFieldPointerFrom(b, "skipped"); err == nil {
		benchFields.skipped = (*bool)(ptr)
	}
	if ptr, err := getFieldPointerFrom(b, "done"); err == nil {
		benchFields.done = (*bool)(ptr)
	}
	if ptr, err := getFieldPointerFrom(b, "finished"); err == nil {
		benchFields.finished = (*bool)(ptr)
	}

	// benchmark
	if ptr, err := getFieldPointerFrom(b, "benchFunc"); err == nil {
		benchFields.benchFunc = (*func(b *testing.B))(ptr)
	}
	if ptr, err := getFieldPointerFrom(b, "result"); err == nil {
		benchFields.result = (*testing.BenchmarkResult)(ptr)
	}

	return benchFields
}

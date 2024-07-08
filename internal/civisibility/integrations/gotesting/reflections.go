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

// TESTING

// getInternalTestArray gets the pointer to the testing.InternalTest array inside a
// testing.M instance containing all the "root" tests
func getInternalTestArray(m *testing.M) *[]testing.InternalTest {
	if ptr, err := getFieldPointerFrom(m, "tests"); err == nil {
		return (*[]testing.InternalTest)(ptr)
	}
	return nil
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

// commonPrivateFields is collection of required private fields from testing.common
type commonPrivateFields struct {
	mu    *sync.RWMutex
	level *int
	name  *string // Name of test or benchmark.
}

// AddLevel increase or decrease the testing.common.level field value, used by
// testing.B to create the name of the benchmark test
func (c *commonPrivateFields) AddLevel(delta int) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	*c.level = *c.level + delta
	return *c.level
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

	// benchmark
	if ptr, err := getFieldPointerFrom(b, "benchFunc"); err == nil {
		benchFields.benchFunc = (*func(b *testing.B))(ptr)
	}
	if ptr, err := getFieldPointerFrom(b, "result"); err == nil {
		benchFields.result = (*testing.BenchmarkResult)(ptr)
	}

	return benchFields
}

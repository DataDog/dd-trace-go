// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gotesting

import (
	"sync"
	"testing"
	"unsafe"
)

// Mock structs to simulate testing.M
type mockTestingM struct {
	deps       testDeps
	tests      []testing.InternalTest
	benchmarks []testing.InternalBenchmark
}

// testDeps is a dummy interface to emulate the memory layout of the original testing.M
// in order to get the right pointers to tests and benchmarks.
type testDeps interface{}

// TestGetFieldPointerFrom tests the getFieldPointerFrom function.
func TestGetFieldPointerFrom(t *testing.T) {
	// Create a mock struct with a private field
	mockStruct := struct {
		privateField string
	}{
		privateField: "testValue",
	}

	// Attempt to get a pointer to the private field
	ptr, err := getFieldPointerFrom(&mockStruct, "privateField")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if ptr == nil {
		t.Fatal("Expected a valid pointer, got nil")
	}

	// Dereference the pointer to get the actual value
	actualValue := (*string)(ptr)
	if *actualValue != mockStruct.privateField {
		t.Fatalf("Expected 'testValue', got %s", *actualValue)
	}

	// Modify the value through the pointer
	*actualValue = "modified value"
	if *actualValue != mockStruct.privateField {
		t.Fatalf("Expected 'testValue', got %s", *actualValue)
	}

	// Attempt to get a pointer to a non-existent field
	_, err = getFieldPointerFrom(&mockStruct, "nonExistentField")
	if err == nil {
		t.Fatal("Expected an error for non-existent field, got nil")
	}
}

// TestGetInternalTestArray tests the getInternalTestArray function.
func TestGetInternalTestArray(t *testing.T) {
	// Create a mock testing.M with a single test
	mockM := &mockTestingM{
		tests: []testing.InternalTest{{Name: "Test1"}},
	}

	// Get the internal test array from the mock testing.M
	tests := getInternalTestArray((*testing.M)(unsafe.Pointer(mockM)))
	if tests == nil {
		t.Fatal("Expected a valid pointer to InternalTest array, got nil")
	}

	// Check that the test array contains the expected test
	if len(*tests) != 1 || (*tests)[0].Name != "Test1" {
		t.Fatalf("Expected a single test named 'Test1', got %+v", *tests)
	}
}

// TestGetInternalBenchmarkArray tests the getInternalBenchmarkArray function.
func TestGetInternalBenchmarkArray(t *testing.T) {
	// Create a mock testing.M with a single benchmark
	mockM := &mockTestingM{
		benchmarks: []testing.InternalBenchmark{{Name: "Benchmark1"}},
	}

	// Get the internal benchmark array from the mock testing.M
	benchmarks := getInternalBenchmarkArray((*testing.M)(unsafe.Pointer(mockM)))
	if benchmarks == nil {
		t.Fatal("Expected a valid pointer to InternalBenchmark array, got nil")
	}

	// Check that the benchmark array contains the expected benchmark
	if len(*benchmarks) != 1 || (*benchmarks)[0].Name != "Benchmark1" {
		t.Fatalf("Expected a single benchmark named 'Benchmark1', got %+v", *benchmarks)
	}
}

// TestCommonPrivateFields_AddLevel tests the AddLevel method of commonPrivateFields.
func TestCommonPrivateFields_AddLevel(t *testing.T) {
	// Create a commonPrivateFields struct with a mutex and a level
	level := 1
	commonFields := &commonPrivateFields{
		mu:    &sync.RWMutex{},
		level: &level,
	}

	// Add a level and check the new level
	newLevel := commonFields.AddLevel(1)
	if newLevel != 2 || newLevel != *commonFields.level {
		t.Fatalf("Expected level to be 2, got %d", newLevel)
	}

	// Subtract a level and check the new level
	newLevel = commonFields.AddLevel(-1)
	if newLevel != 1 || newLevel != *commonFields.level {
		t.Fatalf("Expected level to be 1, got %d", newLevel)
	}
}

// TestGetBenchmarkPrivateFields tests the getBenchmarkPrivateFields function.
func TestGetBenchmarkPrivateFields(t *testing.T) {
	// Create a new testing.B instance
	b := &testing.B{}

	// Get the private fields of the benchmark
	benchFields := getBenchmarkPrivateFields(b)
	if benchFields == nil {
		t.Fatal("Expected a valid benchmarkPrivateFields, got nil")
	}

	// Set values to the private fields
	*benchFields.name = "BenchmarkTest"
	*benchFields.level = 1
	*benchFields.benchFunc = func(b *testing.B) {}
	*benchFields.result = testing.BenchmarkResult{}

	// Check that the private fields have the expected values
	if benchFields.level == nil || *benchFields.level != 1 {
		t.Fatalf("Expected level to be 1, got %v", *benchFields.level)
	}

	if benchFields.name == nil || *benchFields.name != b.Name() {
		t.Fatalf("Expected name to be 'BenchmarkTest', got %v", *benchFields.name)
	}

	if benchFields.benchFunc == nil {
		t.Fatal("Expected benchFunc to be set, got nil")
	}

	if benchFields.result == nil {
		t.Fatal("Expected result to be set, got nil")
	}
}

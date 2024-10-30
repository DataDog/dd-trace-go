// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package coverage

import (
	"reflect"
	"testing"
)

// testStruct is a struct with private and public fields for testing
type testStruct struct {
	publicField  int
	privateField string
}

// mockDeps simulates the 'deps' field with the 'InitRuntimeCoverage' method
type mockDeps struct{}

func (m *mockDeps) InitRuntimeCoverage() (string, func(string, string) (string, error), func() float64) {
	return "test-mode", func(_, _ string) (string, error) { return "tearDownResult", nil }, func() float64 { return 42.0 }
}

// Ensure mockDeps implements testDepsCoverage
var _ testDepsCoverage = &mockDeps{}

// mockM simulates a testing.M struct with a 'deps' field
type mockM struct {
	deps testDepsCoverage
}

func TestGetFieldPointerFrom(t *testing.T) {
	s := testStruct{
		publicField:  42,
		privateField: "secret",
	}

	ptr, err := getFieldPointerFrom(&s, "privateField")
	if err != nil {
		t.Fatalf("getFieldPointerFrom failed: %v", err)
	}
	if ptr == nil {
		t.Fatal("getFieldPointerFrom returned nil pointer")
	}

	// Convert the unsafe pointer to a *string
	strPtr := (*string)(ptr)
	if *strPtr != "secret" {
		t.Errorf("Expected 'secret', got '%s'", *strPtr)
	}

	// Modify the value via the pointer
	*strPtr = "modified"
	if s.privateField != "modified" {
		t.Errorf("Expected 'modified', got '%s'", s.privateField)
	}
}

func TestGetFieldPointerFromValue(t *testing.T) {
	s := testStruct{
		publicField:  42,
		privateField: "secret",
	}

	value := reflect.ValueOf(&s).Elem() // Get the value of s
	ptr, err := getFieldPointerFromValue(value, "privateField")
	if err != nil {
		t.Fatalf("getFieldPointerFromValue failed: %v", err)
	}
	if ptr == nil {
		t.Fatal("getFieldPointerFromValue returned nil pointer")
	}

	// Convert the unsafe pointer to a *string
	strPtr := (*string)(ptr)
	if *strPtr != "secret" {
		t.Errorf("Expected 'secret', got '%s'", *strPtr)
	}

	// Modify the value via the pointer
	*strPtr = "modified"
	if s.privateField != "modified" {
		t.Errorf("Expected 'modified', got '%s'", s.privateField)
	}
}

func TestGetTestDepsCoverage(t *testing.T) {
	m := &mockM{
		deps: &mockDeps{},
	}

	testDepsCov, err := getTestDepsCoverage(m)
	if err != nil {
		t.Fatalf("getTestDepsCoverage failed: %v", err)
	}
	if testDepsCov == nil {
		t.Fatal("getTestDepsCoverage returned nil")
	}

	mode, tearDown, snapcov := testDepsCov.InitRuntimeCoverage()
	if mode != "test-mode" {
		t.Errorf("Expected mode 'test-mode', got '%s'", mode)
	}
	if tearDown == nil {
		t.Error("Expected non-nil tearDown function")
	}
	if snapcov == nil {
		t.Error("Expected non-nil snapcov function")
	}
}

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package dummy

// DummyFunc is a dummy exported function
func DummyFunc() {
	u := DummyStruct{
		unexportedField: 0,
	}
	u.unexportedMethod()
}

// DummyStruct is a dummy exported struct
type DummyStruct struct {
	// ExportedField is an exported field
	ExportedField   string
	unexportedField int
}

// ExportedMethod is an exported method
func (d DummyStruct) ExportedMethod() {}

// unexportedMethod is an unexported method
func (d DummyStruct) unexportedMethod() {}

// AnotherExportedMethod is another exported method
func (d *DummyStruct) AnotherExportedMethod() {}

// DummyInterface is a dummy exported interface
type DummyInterface interface {
	// ExportedMethod is an exported method
	ExportedMethod()
}

// DummyFuncWithParams is a dummy exported function with parameters
func DummyFuncWithParams(_ int, _ string) {
	dummyUnexportedFunc()
}

// ArrayTestType is a type containing array fields for testing array type formatting
type ArrayTestType struct {
	FixedArray    [16]byte
	MultiDimArray [2][3]int
}

// EmptyStruct is an empty struct with methods (like NoopTracer)
type EmptyStruct struct{}

// DoSomething is a method on EmptyStruct
func (EmptyStruct) DoSomething() string {
	return "something"
}

// DoSomethingElse is another method on EmptyStruct with pointer receiver
func (*EmptyStruct) DoSomethingElse() int {
	return 42
}

// MyString is a type alias for string
type MyString string

// MyInt is a type alias for int
type MyInt int

// dummyUnexportedFunc is an unexported function
func dummyUnexportedFunc() {}

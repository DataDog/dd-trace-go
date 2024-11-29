// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package coverage

import (
	"errors"
	"reflect"
	"unsafe"
)

// testDepsCoverage is an interface to support runtime coverage initialization from the original testDeps testing interface
type testDepsCoverage interface {
	InitRuntimeCoverage() (mode string, tearDown func(coverprofile string, gocoverdir string) (string, error), snapcov func() float64)
}

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

// getTestDepsCoverage gets the testDepsCoverage interface from a testing.M instance
func getTestDepsCoverage(m any) (testDepsCoverage, error) {
	// let's first do some signature checks to avoid panics before calling the method
	reflectDeps := reflect.ValueOf(m).Elem().FieldByName("deps")
	reflectInitRuntimeCoverage := reflectDeps.MethodByName("InitRuntimeCoverage")
	if !reflectInitRuntimeCoverage.IsValid() {
		return nil, errors.New("InitRuntimeCoverage not found")
	}

	reflectInitRuntimeCoverageType := reflectInitRuntimeCoverage.Type()
	if reflectInitRuntimeCoverageType.NumIn() != 0 {
		return nil, errors.New("InitRuntimeCoverage has arguments (this signature is not supported)")
	}
	if reflectInitRuntimeCoverageType.NumOut() != 3 {
		return nil, errors.New("InitRuntimeCoverage has an unexpected number of return values")
	}
	if reflectInitRuntimeCoverageType.Out(0).String() != "string" {
		return nil, errors.New("InitRuntimeCoverage has an unexpected return type")
	}
	if reflectInitRuntimeCoverageType.Out(1).String() != "func(string, string) (string, error)" {
		return nil, errors.New("InitRuntimeCoverage has an unexpected return type")
	}
	if reflectInitRuntimeCoverageType.Out(2).String() != "func() float64" {
		return nil, errors.New("InitRuntimeCoverage has an unexpected return type")
	}

	// now we can safely call the method
	ptr, err := getFieldPointerFrom(m, "deps")
	if err != nil {
		return nil, err
	}

	if ptr == nil {
		return nil, errors.New("testDepsCoverage not found")
	}

	tDepValue := reflect.NewAt(reflect.TypeFor[testDepsCoverage](), ptr)
	return tDepValue.Elem().Interface().(testDepsCoverage), nil
}

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package coverage

import (
	"errors"
	"reflect"
	"testing"
	"unsafe"
)

// testDepsCoverage is an interface to support runtime coverage initialization from the original testDeps testing interface
type testDepsCoverage interface {
	InitRuntimeCoverage() (mode string, tearDown func(coverprofile string, gocoverdir string) (string, error), snapcov func() float64)
}

//go:linkname getFieldPointerFrom gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations/gotesting.getFieldPointerFrom
func getFieldPointerFrom(value any, fieldName string) (unsafe.Pointer, error)

// getTestDepsCoverage gets the testDepsCoverage interface from a testing.M instance
func getTestDepsCoverage(m *testing.M) (testDepsCoverage, error) {
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
	if reflectInitRuntimeCoverageType.Out(0).String() == "string" {
		return nil, errors.New("InitRuntimeCoverage has an unexpected return type")
	}
	if reflectInitRuntimeCoverageType.Out(1).String() == "func(string, string) (string, error)" {
		return nil, errors.New("InitRuntimeCoverage has an unexpected return type")
	}
	if reflectInitRuntimeCoverageType.Out(2).String() == "func() float64" {
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
